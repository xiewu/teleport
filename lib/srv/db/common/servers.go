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

import "github.com/gravitational/teleport/api/types"

// DatabaseServerStatus define the status of a database server given a database.
// The codes are organized in order, where the smallest number represents a
// healthy server.
type DatabaseServerStatus int

const (
	DatabaseServerStatusHealthy DatabaseServerStatus = iota
	DatabaseServerStatusWarning
	DatabaseServerStatusUnknown
	DatabaseServerStatusUnhealthy
)

// TODO: make it easier to read through
//
// assuming we'll have only 3 checks:
// - HEALTHY:
//   - last two checks succeeded
//   - last and third checks succeeded (TODO)
//
// - WARNING:
//   - last succeeded but all other two failed
//
// - UNHEALTHY:
//   - all failed
func ServerStatus(server types.DatabaseServer) DatabaseServerStatus {
	checks := server.GetDatabase().GetHealthchecks()
	totalChecks := len(checks)
	if totalChecks == 0 {
		return DatabaseServerStatusUnknown
	}

	lastSucceded := checks[0].IsSuccess()
	if totalChecks == 1 {
		if lastSucceded {
			return DatabaseServerStatusHealthy
		}

		return DatabaseServerStatusUnhealthy
	}

	totalSucceeded := 0
	if lastSucceded {
		totalSucceeded += 1
	}

	for _, check := range checks[1:] {
		if check.IsSuccess() {
			totalSucceeded += 1
		}
	}

	switch {
	case lastSucceded && totalSucceeded == 0:
		return DatabaseServerStatusWarning
	case totalSucceeded < 2:
		return DatabaseServerStatusUnhealthy
	}

	return DatabaseServerStatusHealthy
}
