/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// The webhook certificate's lifetimes (docs/plans/api-versions.md, decision 3):
// the serving certificate lasts a year and is re-issued with under 30 days
// left; the CA that signs it lasts ten, so a re-issued serving certificate is
// still trusted by the caBundle every CRD holds and by a replica that has not
// yet read the new one.
const (
	webhookCertLifetime = 365 * 24 * time.Hour
	webhookCALifetime   = 10 * webhookCertLifetime
	webhookCertRenewal  = 30 * 24 * time.Hour
	// WebhookCertRecheck is how often a running operator reads the Secret
	// again, re-issuing what is under webhookCertRenewal and serving what
	// another replica wrote.
	WebhookCertRecheck = 24 * time.Hour
)

// The webhook certificate Secret's keys: tls.crt and tls.key as a
// kubernetes.io/tls Secret has them, and the CA beside them.
const (
	webhookCAKey     = "ca.crt"
	webhookCAPrivKey = "ca.key"
)

// WebhookCRDs are the CRDs whose conversion webhook the operator serves: every
// kind of c4.factory.io (a test keeps the list equal to config/crd/bases). The
// RBAC marker below names the same CRDs.
var WebhookCRDs = []string{
	"agents.c4.factory.io", "briefs.c4.factory.io", "bundles.c4.factory.io", "computes.c4.factory.io",
	"connectors.c4.factory.io", "containers.c4.factory.io", "crews.c4.factory.io",
	"datastores.c4.factory.io", "deployments.c4.factory.io", "deploytargets.c4.factory.io",
	"frameworks.c4.factory.io", "gitproviders.c4.factory.io", "people.c4.factory.io",
	"projects.c4.factory.io", "relationships.c4.factory.io", "sessions.c4.factory.io",
	"softwaresystems.c4.factory.io", "standards.c4.factory.io", "tasks.c4.factory.io",
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;patch,resourceNames=agents.c4.factory.io;briefs.c4.factory.io;bundles.c4.factory.io;computes.c4.factory.io;connectors.c4.factory.io;containers.c4.factory.io;crews.c4.factory.io;datastores.c4.factory.io;deployments.c4.factory.io;deploytargets.c4.factory.io;frameworks.c4.factory.io;gitproviders.c4.factory.io;people.c4.factory.io;projects.c4.factory.io;relationships.c4.factory.io;sessions.c4.factory.io;softwaresystems.c4.factory.io;standards.c4.factory.io;tasks.c4.factory.io

// WebhookCert keeps the webhook server's certificate: it reads the Secret
// Namespace/Secret or issues a CA and a serving certificate for
// Service.Namespace.svc into it, and patches the CA into the conversion stanza
// of every CRD in WebhookCRDs that converts by webhook. The webhook server
// serves it through GetCertificate. Run Ensure once before the manager starts;
// as a manager Runnable it checks again every Recheck.
type WebhookCert struct {
	// Client reads and writes without a cache: Ensure runs before the
	// manager's caches start.
	Client    client.Client
	Namespace string
	Secret    string
	Service   string
	// CRDs defaults to WebhookCRDs.
	CRDs []string
	// Recheck defaults to WebhookCertRecheck.
	Recheck time.Duration
	// Now defaults to time.Now.
	Now func() time.Time

	served atomic.Pointer[tls.Certificate]
}

// Host is the name the serving certificate is for, the Service's in-cluster
// DNS name.
func (w *WebhookCert) Host() string {
	return fmt.Sprintf("%s.%s.svc", w.Service, w.Namespace)
}

// GetCertificate serves the certificate Ensure loaded; it is the webhook
// server's tls.Config.GetCertificate.
func (w *WebhookCert) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if c := w.served.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("no webhook certificate loaded yet")
}

// NeedLeaderElection is false: every replica serves the webhook.
func (w *WebhookCert) NeedLeaderElection() bool { return false }

// Start checks the certificate every Recheck until ctx ends; a failed check is
// logged and tried again at the next one, the loaded certificate served
// meanwhile.
func (w *WebhookCert) Start(ctx context.Context) error {
	every := w.Recheck
	if every <= 0 {
		every = WebhookCertRecheck
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.Ensure(ctx); err != nil {
				logf.FromContext(ctx).Error(err, "Failed to check the webhook certificate", "secret", w.Secret)
			}
		}
	}
}

// Ensure reads the Secret, issuing or re-issuing what it lacks, loads the
// serving certificate for GetCertificate and patches the CA into the CRDs.
func (w *WebhookCert) Ensure(ctx context.Context) error {
	data, err := w.secretData(ctx)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(data[corev1.TLSCertKey], data[corev1.TLSPrivateKeyKey])
	if err != nil {
		return fmt.Errorf("load the webhook certificate from Secret %s: %w", w.Secret, err)
	}
	w.served.Store(&pair)
	return w.patchCRDs(ctx, data[webhookCAKey])
}

// secretData returns the Secret's data once it holds a CA and a serving
// certificate valid past the renewal window, creating or updating it when it
// doesn't. A conflict means another replica wrote it first: read again and
// take theirs.
func (w *WebhookCert) secretData(ctx context.Context) (map[string][]byte, error) {
	key := client.ObjectKey{Namespace: w.Namespace, Name: w.Secret}
	for range 3 {
		var secret corev1.Secret
		err := w.Client.Get(ctx, key, &secret)
		switch {
		case apierrors.IsNotFound(err):
			data, err := issueWebhookCert(nil, w.Host(), w.now())
			if err != nil {
				return nil, err
			}
			secret = corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: w.Namespace, Name: w.Secret},
				Type:       corev1.SecretTypeTLS,
				Data:       data,
			}
			if err := w.Client.Create(ctx, &secret); apierrors.IsAlreadyExists(err) {
				continue
			} else if err != nil {
				return nil, fmt.Errorf("create the webhook certificate Secret %s: %w", w.Secret, err)
			}
			logf.FromContext(ctx).Info("Issued the webhook certificate", "secret", w.Secret, "host", w.Host())
			return data, nil
		case err != nil:
			return nil, fmt.Errorf("read the webhook certificate Secret %s: %w", w.Secret, err)
		}
		if webhookCertUsable(secret.Data, w.Host(), w.now()) {
			return secret.Data, nil
		}
		data, err := issueWebhookCert(secret.Data, w.Host(), w.now())
		if err != nil {
			return nil, err
		}
		secret.Data = data
		if err := w.Client.Update(ctx, &secret); apierrors.IsConflict(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("update the webhook certificate Secret %s: %w", w.Secret, err)
		}
		logf.FromContext(ctx).Info("Re-issued the webhook certificate", "secret", w.Secret, "host", w.Host())
		return data, nil
	}
	return nil, fmt.Errorf("the webhook certificate Secret %s kept changing under us", w.Secret)
}

// patchCRDs sets the CA as the caBundle of every CRD that converts by webhook.
// A CRD with no webhook conversion is left as it is: the API server refuses a
// webhook stanza beside strategy None.
func (w *WebhookCert) patchCRDs(ctx context.Context, ca []byte) error {
	names := w.CRDs
	if names == nil {
		names = WebhookCRDs
	}
	for _, name := range names {
		var crd apiextensionsv1.CustomResourceDefinition
		if err := w.Client.Get(ctx, client.ObjectKey{Name: name}, &crd); err != nil {
			return fmt.Errorf("read CRD %s: %w", name, err)
		}
		conv := crd.Spec.Conversion
		if conv == nil || conv.Strategy != apiextensionsv1.WebhookConverter || conv.Webhook == nil ||
			conv.Webhook.ClientConfig == nil || bytes.Equal(conv.Webhook.ClientConfig.CABundle, ca) {
			continue
		}
		base := crd.DeepCopy()
		conv.Webhook.ClientConfig.CABundle = ca
		if err := w.Client.Patch(ctx, &crd, client.MergeFrom(base)); err != nil {
			return fmt.Errorf("patch the CA into CRD %s: %w", name, err)
		}
		logf.FromContext(ctx).Info("Patched the webhook CA into a CRD", "crd", name)
	}
	return nil
}

func (w *WebhookCert) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// webhookCertUsable reports whether data holds a CA and a serving certificate
// for host that it signed, both valid for longer than the renewal window.
func webhookCertUsable(data map[string][]byte, host string, now time.Time) bool {
	ca, _, err := parseWebhookCA(data, now)
	if err != nil {
		return false
	}
	if _, err := tls.X509KeyPair(data[corev1.TLSCertKey], data[corev1.TLSPrivateKeyKey]); err != nil {
		return false
	}
	cert, err := parseCertPEM(data[corev1.TLSCertKey])
	if err != nil || cert.NotAfter.Sub(now) < webhookCertRenewal || cert.VerifyHostname(host) != nil {
		return false
	}
	return cert.CheckSignatureFrom(ca) == nil
}

// parseWebhookCA returns the CA in data when it is valid for longer than the
// renewal window.
func parseWebhookCA(data map[string][]byte, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	ca, err := parseCertPEM(data[webhookCAKey])
	if err != nil {
		return nil, nil, err
	}
	if ca.NotAfter.Sub(now) < webhookCertRenewal {
		return nil, nil, errors.New("the webhook CA expires soon")
	}
	block, _ := pem.Decode(data[webhookCAPrivKey])
	if block == nil {
		return nil, nil, errors.New("no CA key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return ca, key, nil
}

func parseCertPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// issueWebhookCert returns Secret data with a serving certificate for host,
// valid webhookCertLifetime, signed by the CA in old when that is still valid
// past the renewal window, else by a new CA.
func issueWebhookCert(old map[string][]byte, host string, now time.Time) (map[string][]byte, error) {
	data := map[string][]byte{}
	ca, caKey, err := parseWebhookCA(old, now)
	if err == nil {
		data[webhookCAKey], data[webhookCAPrivKey] = old[webhookCAKey], old[webhookCAPrivKey]
	} else {
		caKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		tmpl := &x509.Certificate{
			Subject:               pkix.Name{CommonName: "c4-factory webhook CA"},
			NotBefore:             now.Add(-time.Hour),
			NotAfter:              now.Add(webhookCALifetime),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		}
		if ca, data[webhookCAKey], err = signCert(tmpl, nil, &caKey.PublicKey, caKey); err != nil {
			return nil, err
		}
		if data[webhookCAPrivKey], err = encodeECKey(caKey); err != nil {
			return nil, err
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		Subject:     pkix.Name{CommonName: host},
		DNSNames:    []string{host},
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(webhookCertLifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, data[corev1.TLSCertKey], err = signCert(tmpl, ca, &key.PublicKey, caKey); err != nil {
		return nil, err
	}
	if data[corev1.TLSPrivateKeyKey], err = encodeECKey(key); err != nil {
		return nil, err
	}
	return data, nil
}

// signCert signs tmpl with signer as parent, or self-signs it when parent is
// nil, returning the certificate and its PEM.
func signCert(tmpl, parent *x509.Certificate, pub *ecdsa.PublicKey, signer *ecdsa.PrivateKey) (
	*x509.Certificate, []byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl.SerialNumber = serial
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

func encodeECKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}
