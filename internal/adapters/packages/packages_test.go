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

package packages

import "testing"

// TestSdistDir checks which top directory makes a tar's PKG-INFO an sdist's:
// <name>-<version>, in any spelling of the name and form of the version (#1701).
func TestSdistDir(t *testing.T) {
	for _, tt := range []struct {
		dir, name, version string
		want               bool
	}{
		{"shop_api-0.1.0", "shop-api", "0.1.0", true},
		{"shop-api-0.1.0", "shop-api", "0.1.0", true},
		{"Shop.API-0.1.0", "shop_api", "0.1.0", true},
		{"shop_api-1.0.0-RC.1", "shop-api", "1.0.0rc1", true},
		{"shop_api-1.0.0rc1", "shop-api", "1.0.0-RC.1", true},
		{"docs", "shop-api", "0.1.0", false},
		{"shop_api", "shop-api", "0.1.0", false},
		{"shop_api-0.2.0", "shop-api", "0.1.0", false},
		{"other-0.1.0", "shop-api", "0.1.0", false},
		{"shop_api-0.1.0", "shop-api", "not a version", false},
	} {
		if got := sdistDir(tt.dir, tt.name, tt.version); got != tt.want {
			t.Errorf("sdistDir(%q, %q, %q) = %v, want %v", tt.dir, tt.name, tt.version, got, tt.want)
		}
	}
}
