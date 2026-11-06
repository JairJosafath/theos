package awsenv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// probeTimeout bounds one probe of theos-env on a machine.
const probeTimeout = 10 * time.Second

// PinnedProbe asks theos-env at endpoint for /healthz over TLS, and takes
// the answer only from a server whose certificate has the key keyPin names:
// the lease's own theos-env, whatever the machine's address. The probe
// carries no token; /healthz needs none.
func PinnedProbe(ctx context.Context, endpoint, keyPin string) error {
	want, err := base64.RawURLEncoding.DecodeString(keyPin)
	if err != nil || len(want) != ed25519.PublicKeySize {
		return errors.New("the machine carries no theos-env key to check it by")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12,
		// The certificate names no host (model.TestEnvCertificate): it is
		// checked by its key below instead.
		// VerifyConnection runs on a resumed session too.
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection pins the key
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("the server sent no certificate")
			}
			if key, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey); !ok || !bytes.Equal(key, want) {
				return errors.New("the server's certificate isn't the lease's")
			}
			return nil
		}}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("theos-env answered %s", resp.Status)
	}
	return nil
}
