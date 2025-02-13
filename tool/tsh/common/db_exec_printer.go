/*
 * Teleport
 * Copyright (C) 2025  Gravitational, Inc.
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
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gravitational/teleport/api/types"
	"github.com/gravitational/teleport/lib/utils"
)

type ansiEscapeCode string

const (
	colorGreen  ansiEscapeCode = "\033[32m"
	colorYellow ansiEscapeCode = "\033[33m"
	colorBlue   ansiEscapeCode = "\033[34m"
	colorReset  ansiEscapeCode = "\033[0m"
)

type databaseExecPrinter struct {
	io.Writer
	name  string
	color ansiEscapeCode
}

func (w *databaseExecPrinter) Write(p []byte) (n int, err error) {
	for _, line := range strings.Split(string(p), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		msg := fmt.Sprintf("%v %s: ", time.Now().Format(time.RFC3339), w.name)
		if w.color != "" {
			msg = string(w.color) + msg + string(colorReset)
		}
		msg += trimmed
		fmt.Fprintln(w.Writer, msg)
	}
	return len(p), nil
}

func pickColorIfTerminal(color ansiEscapeCode) ansiEscapeCode {
	if utils.IsTerminal(os.Stderr) {
		return color
	}
	return ""
}

func newDatabaseExecInfoPrinter(cf *CLIConf) io.Writer {
	return &databaseExecPrinter{
		Writer: cf.Stdout(),
		name:   "[info]",
		color:  pickColorIfTerminal(colorBlue),
	}
}

func newDatabaseExecOutputPrinter(cf *CLIConf, db types.Database) io.Writer {
	return &databaseExecPrinter{
		Writer: cf.Stdout(),
		name:   fmt.Sprintf("[%s][output]", db.GetName()),
		color:  pickColorIfTerminal(colorGreen),
	}
}

func newDatabaseExecErrorPrinter(cf *CLIConf, db types.Database) io.Writer {
	return &databaseExecPrinter{
		Writer: cf.Stderr(),
		name:   fmt.Sprintf("[%s][error]", db.GetName()),
		color:  pickColorIfTerminal(colorYellow),
	}
}
