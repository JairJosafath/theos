# Plan: the android and godot images, and test environments in the UI

Carried over from [done/test-environments.md](done/test-environments.md)
(theos#12, c4factory #2036), whose steps 5 and 6 weren't built when it was
closed. That plan's *Done when* was met with its steps 1 to 4 and the remote
providers of steps 7 to 10; these two steps add images and a view, and
change no rule of it. Its decisions (T1 to T14) hold here as written.

## Goal

A project gets an Android emulator and a Godot engine on its own nodes
from images the release publishes, as it gets the browser, and a person
sees a project's test environments, their live leases, today's spend and
their limits, and a run's leases, in the UI.

## Steps

1. **Devices on the host: `android` and `godot`** (samples and
   `hack/env-android`, `hack/env-godot`, pinned, published with the
   release as `env-browser` is). Table tests: the pod spec carries the
   extended resources (`devices.kubevirt.io/kvm`, `nvidia.com/gpu`) and
   `runtimeClassName` as given (`localenv`'s
   `TestBuildPodPlacesAndRequestsDevices` already covers the spec; the
   samples add none). `make validate-yaml` checks both samples, and
   `hack/check-pinned-images.sh` their bases. Whether KVM and the GPU work
   is for a human's run on a node that has them.
   *Done when:* those tests and `make check` pass.
2. **The UI** (`ui/`). The Project area lists its TestEnvironments with
   their member, `Ready` and `CredentialsValid`, live leases, today's spend
   against `budgetUSDPerDay`, the `CI` condition and a deviceFarm
   environment's last test runs (`status.testRuns`); the Task page shows a
   run's leases (`status.environments`). UI tests on a demo fixture.
   *Done when:* the UI tests pass and a screenshot is in
   `docs/screenshots/`.

## Done when

Both steps are merged with their tests and `make check` passes.
