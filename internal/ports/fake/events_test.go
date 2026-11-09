/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fake_test

import (
	"fmt"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// TestEventsContract runs the Events contract suite on the fake, whose
// deliveries arrive in its outbox.
func TestEventsContract(t *testing.T) {
	g := fake.New()
	owners := 0
	owner := func(h ports.GitHost) string { return h.Repository("any").Owner }
	contract.Events(t, contract.EventsEnv{
		New: func(*testing.T) ports.GitHost {
			owners++
			return g.For(ports.Config{URL: "http://git.test", Owner: fmt.Sprintf("events-%d", owners)})
		},
		Receiver: func(_ *testing.T, h ports.GitHost) contract.Receiver {
			return contract.Receiver{
				Bases: [2]string{"http://receiver-a.test:8082", "http://receiver-b.test:8082"},
				Received: func() []contract.Delivery {
					deliveries := g.Deliveries(owner(h))
					out := make([]contract.Delivery, 0, len(deliveries))
					for _, d := range deliveries {
						out = append(out, contract.Delivery{URL: d.URL, Body: d.Body,
							Header: func(name string) string { return d.Header[name] }})
					}
					return out
				},
			}
		},
		Hooks: func(_ *testing.T, h ports.GitHost) []string {
			hooks := g.EventHooks(owner(h))
			out := make([]string, 0, len(hooks))
			for _, hook := range hooks {
				out = append(out, hook.URL)
			}
			return out
		},
	})
}
