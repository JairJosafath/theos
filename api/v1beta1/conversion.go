/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1beta1

// v1beta1 is the hub every other served version converts through
// (docs/plans/done/api-versions.md, decision 3).

// Hub marks Agent as the conversion hub.
func (*Agent) Hub() {}

// Hub marks Brief as the conversion hub.
func (*Brief) Hub() {}

// Hub marks Bundle as the conversion hub.
func (*Bundle) Hub() {}

// Hub marks Compute as the conversion hub.
func (*Compute) Hub() {}

// Hub marks Connector as the conversion hub.
func (*Connector) Hub() {}

// Hub marks Container as the conversion hub.
func (*Container) Hub() {}

// Hub marks Crew as the conversion hub.
func (*Crew) Hub() {}

// Hub marks DataStore as the conversion hub.
func (*DataStore) Hub() {}

// Hub marks DeployTarget as the conversion hub.
func (*DeployTarget) Hub() {}

// Hub marks Deployment as the conversion hub.
func (*Deployment) Hub() {}

// Hub marks Domain as the conversion hub.
func (*Domain) Hub() {}

// Hub marks Framework as the conversion hub.
func (*Framework) Hub() {}

// Hub marks GitProvider as the conversion hub.
func (*GitProvider) Hub() {}

// Hub marks Person as the conversion hub.
func (*Person) Hub() {}

// Hub marks Project as the conversion hub.
func (*Project) Hub() {}

// Hub marks Relationship as the conversion hub.
func (*Relationship) Hub() {}

// Hub marks Session as the conversion hub.
func (*Session) Hub() {}

// Hub marks SoftwareSystem as the conversion hub.
func (*SoftwareSystem) Hub() {}

// Hub marks Standard as the conversion hub.
func (*Standard) Hub() {}

// Hub marks Task as the conversion hub.
func (*Task) Hub() {}
