/*
 * Teleport
 * Copyright (C) 2024  Gravitational, Inc.
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package common

import (
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/utils"
)

func NewConnectivityHealthcheck(connErr error, details string) types.DatabaseHealthCheckV1 {
	if connErr != nil && utils.IsOKNetworkError(connErr) {
		// normal network teardown errors should not indicate unhealthy db
		// connectivity.
		connErr = nil
	}

	diagnostic := &types.ConnectionDiagnosticSpecV1{
		Success: true,
		Message: types.DiagnosticMessageSuccess,
		Traces: []*types.ConnectionDiagnosticTrace{
			types.NewTraceDiagnosticConnection(
				types.ConnectionDiagnosticTrace_CONNECTIVITY,
				details,
				connErr,
			),
		},
	}
	if connErr != nil {
		diagnostic.Success = false
		diagnostic.Message = types.DiagnosticMessageFailed
	}

	return types.DatabaseHealthCheckV1{
		Time:       time.Now(),
		Diagnostic: diagnostic,
	}
}

func PrependHealthCheck(db types.Database, check types.DatabaseHealthCheckV1) {
	health := db.GetStatusHealth()
	checks := make([]*types.DatabaseHealthCheckV1, 0, min(3, len(health.Checks)+1))
	checks[0] = &check
	if len(health.Checks) > 0 {
		checks = append(checks, health.Checks[:cap(checks)-1]...)
	}
	health.Checks = checks
	db.SetStatusHealth(health)
}
