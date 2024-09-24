// Teleport
// Copyright (C) 2024 Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package common

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types"
)

func TestServerStatus(t *testing.T) {
	for name, tc := range map[string]struct {
		checks         []bool
		expectedStatus DatabaseServerStatus
	}{
		"all success":          {[]bool{true, true, true}, DatabaseServerStatusHealthy},
		"2nd failed":           {[]bool{true, false, true}, DatabaseServerStatusHealthy},
		"last two failed":      {[]bool{false, false, true}, DatabaseServerStatusUnhealthy},
		"all failure":          {[]bool{false, false, false}, DatabaseServerStatusUnhealthy},
		"single success check": {[]bool{true}, DatabaseServerStatusHealthy},
		"single failure check": {[]bool{false}, DatabaseServerStatusUnhealthy},
		"unknown":              {[]bool{}, DatabaseServerStatusUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			var checks []*types.DatabaseHealthCheckV1
			for _, success := range tc.checks {
				checks = append(checks, &types.DatabaseHealthCheckV1{
					Diagnostic: &types.ConnectionDiagnosticSpecV1{
						Success: success,
					},
				})
			}

			server := &types.DatabaseServerV3{
				Spec: types.DatabaseServerSpecV3{
					Database: &types.DatabaseV3{
						Status: types.DatabaseStatusV3{
							Health: &types.DatabaseHealthV1{
								HealthChecks: checks,
							},
						},
					},
				},
			}
			require.Equal(t, tc.expectedStatus, ServerStatus(server))
		})
	}
}
