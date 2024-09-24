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
	"time"

	"github.com/gravitational/teleport/api/types"
)

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

// ServerStatus returns server status and update time.
// Healthy: all checks went fine.
// Warning: some checks went fine.
// Unhealthy: none checks went fine.
// Unknown: there were no checks so far.
func ServerStatus(server types.DatabaseServer) (DatabaseServerStatus, time.Time) {
	checks := server.GetDatabase().GetHealthchecks()
	totalChecks := len(checks)
	if totalChecks == 0 {
		return DatabaseServerStatusUnknown, time.Now()
	}

	succeeded := 0
	var latest time.Time

	for _, check := range checks {
		if check.Time.After(latest) {
			latest = check.Time
		}
		if check.Diagnostic.Success {
			succeeded++
		}
	}

	if succeeded == totalChecks {
		return DatabaseServerStatusHealthy, latest
	}

	if succeeded == 0 {
		return DatabaseServerStatusUnhealthy, latest
	}

	return DatabaseServerStatusWarning, latest
}
