---
name: secrets
description: Use when a change in the C4 Factory makes, stores, passes or tests a credential (password, token, key, Kubernetes Secret) in Helm chart templates, config/ manifests, make targets, scripts, the seed, Go tests or samples, or when a secret scanner such as GitGuardian fails on a pull request.
---

# Secrets

The repository and everything Helm renders hold no credential values. A
credential the factory can make is made in the cluster, at runtime, by the
component that needs it. A credential only the user has (a GitHub token) comes
in a Secret the user creates; the factory takes its name.

Why: Helm keeps every rendered manifest in its release record, a Secret in the
release namespace, for every revision. `lookup` is empty under `helm template`,
`--dry-run`, Argo CD and Flux, so a value made in a template changes on each of
those renders. A literal in git stays in its history.

## Where a credential comes from

| Place | Recipe |
| --- | --- |
| Chart or `config/` manifests | Pods reference the Secret by name (`secretKeyRef`). The pod that needs it makes it in a first init container, below. `existingSecret` names the user's own Secret, which that container leaves alone like any existing one. |
| A token a running service issues (Gitea API tokens) | The job that talks to the service makes it and stores it with `kubectl create secret`, as `hack/seed/seed.sh` does. |
| make targets and scripts | `head -c 24 /dev/urandom \| base64` into `kubectl create secret generic`, only when the Secret is missing (target `gitea` in `hack/testenv.mk`). |
| A credential only the user has | The user creates the Secret; the chart takes `existingSecret`. |
| Go tests | `token := rand.Text()` (`crypto/rand`) in the fixture; assert against `token`. |
| UI (Node) tests | `randomBytes(18).toString('base64url')` (`node:crypto`), the same way. |

The init container, for a pod that needs Secret `gitea-admin`:

```yaml
spec:
  serviceAccountName: gitea
  automountServiceAccountToken: false # Gitea itself gets no API access
  initContainers:
    - name: credentials # first; never overwrites
      image: <an image with kubectl, e.g. the seed image>
      command: [sh, -ec]
      args:
        - |
          ensure() { kubectl get secret "$1" >/dev/null 2>&1 ||
            kubectl create secret generic "$1" --from-literal="$2=$(head -c 24 /dev/urandom | base64)"; }
          ensure gitea-admin password
      volumeMounts:
        - {name: api, mountPath: /var/run/secrets/kubernetes.io/serviceaccount, readOnly: true}
  volumes:
    - name: api
      projected:
        sources:
          - serviceAccountToken: {path: token}
          - configMap: {name: kube-root-ca.crt, items: [{key: ca.crt, path: ca.crt}]}
          - downwardAPI: {items: [{path: namespace, fieldRef: {fieldPath: metadata.namespace}}]}
```

Its Role, in the pod's namespace: `create` on `secrets`, and `get` on `secrets`
with `resourceNames` listing the Secrets it makes. Rotation: delete the Secret
and restart the pod.

## When a scanner flags a pull request

The finding is a credential in the repository or in what Helm renders. Fix the
design with the table above, push, and wait for the check to pass. If the fix
doesn't fit in the session, hand off with the finding and the fix.

Never an ignore rule (`.gitguardian.yaml`, dashboard exclusions), a
false-positive mark, a rename that hides the name from the detector, or a
request that a person do one of these.

| Thought | Reality |
| --- | --- |
| "No literal is committed; `randAlphaNum` makes it at install" | The value sits in Helm's release record, and `helm template` makes a new one each time. |
| "It's a false positive" | The scanner found a credential made by a template. That is the bug. |
| "The plan or the existing code does it this way" | Fix the plan or the code too; this skill wins. |
| "GitOps users can set `existingSecret`" | The pod makes its own; it works under `helm template`. |
| "No time left; a person can mark it" | Hand off with the fix. A mark fixes nothing. |

## Check

- `helm template` with every option on: no generated value in the output;
  each Secret a pod uses is referenced, not rendered.
- `grep -rn -i -E '(password|token|secret)[a-z]*"?\s*[:=]+\s*"[^"{$]{3,}"'`
  over your diff finds no credential literal.
