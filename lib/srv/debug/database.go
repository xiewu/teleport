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

package debug

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

func handleGetDatabaseHealthCheck(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := getDatabaseServicePlugin()
		if service == nil {
			http.Error(w, "database service is not registered", http.StatusServiceUnavailable)
			return
		}

		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "database name not provided", http.StatusBadRequest)
			return
		}

		db, err := service.GetProxiedDatabase(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		health := db.GetStatusHealth()
		data, err := json.MarshalIndent(health, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(data)
		return
	}
}

func handleRunDatabaseHealthCheck(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		service := getDatabaseServicePlugin()
		if service == nil {
			http.Error(w, "database service is not registered", http.StatusServiceUnavailable)
			return
		}

		name := r.URL.Query().Get("name")
		if name == "" {
			http.Error(w, "database name not provided", http.StatusBadRequest)
			return
		}

		db, err := service.RunHealthCheck(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		health := db.GetStatusHealth()
		data, err := json.MarshalIndent(health, "", "  ")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write(data)
		return
	}
}
