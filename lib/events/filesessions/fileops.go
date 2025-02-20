// Teleport
// Copyright (C) 2025 Gravitational, Inc.
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

package filesessions

import (
	"context"
	"io"
	"log/slog"
	"os"

	"filippo.io/age"
	"github.com/gravitational/trace"

	"github.com/gravitational/teleport/lib/utils"
)

const (
	reservationFilePerm = 0600
	combinedFilePerm    = reservationFilePerm
)

// FileOps captures file operations done by filesessions, allowing both
// plaintext and encrypted implementations to co-exist.
type FileOps interface {
	CreateReservation(name string, size int64) error
	WriteReservation(name string, data io.Reader) error
	CombineParts(dst io.Writer, parts []string) error
}

type plainFileOps struct {
	Logger   *slog.Logger
	OpenFile utils.OpenFileWithFlagsFunc
}

var _ FileOps = &plainFileOps{}

func (p *plainFileOps) CreateReservation(name string, size int64) (err error) {
	defer func() {
		if err != nil {
			err = trace.ConvertSystemError(err)
		}
	}()

	f, err := p.OpenFile(name, os.O_WRONLY|os.O_CREATE, reservationFilePerm)
	if err != nil {
		return trace.Wrap(err)
	}

	if err := f.Truncate(size); err != nil {
		loggingClose(f, p.Logger, "Failed to close file (Truncate error flow)", "name", name)
		return trace.Wrap(err)
	}

	return trace.Wrap(f.Close())
}

func (p *plainFileOps) WriteReservation(name string, data io.Reader) (err error) {
	defer func() {
		if err != nil {
			err = trace.ConvertSystemError(err)
		}
	}()

	// O_CREATE kept for backwards compatibility only.
	const flag = os.O_WRONLY | os.O_CREATE

	f, err := p.OpenFile(name, flag, reservationFilePerm)
	if err != nil {
		return trace.Wrap(err)
	}

	n, err := io.Copy(f, data)
	if err != nil {
		loggingClose(f, p.Logger, "Failed to close file (io.Copy error flow)", "name", name)
		return trace.Wrap(err)
	}

	// Truncate to n so the file has the correct size at the end. Files are
	// pre-reserved with relatively large sizes by CreateReservation first.
	if err := f.Truncate(n); err != nil {
		loggingClose(f, p.Logger, "Failed to close file (Truncate error flow)", "name", name)
		return trace.Wrap(err)
	}

	return trace.Wrap(f.Close())
}

func (p *plainFileOps) CombineParts(dst io.Writer, parts []string) (err error) {
	if err := combineParts(dst, parts, p.OpenFile, p.Logger); err != nil {
		return trace.ConvertSystemError(err)
	}
	return nil
}

type encryptedFileOps struct {
	Logger     *slog.Logger
	OpenFile   utils.OpenFileWithFlagsFunc
	Recipients []age.Recipient
}

var _ FileOps = &encryptedFileOps{}

func (e *encryptedFileOps) CreateReservation(name string, size int64) error {
	return e.plaintext().CreateReservation(name, size)
}

func (e *encryptedFileOps) WriteReservation(name string, data io.Reader) error {
	// TODO(codingllama): Encrypt reservations with an additional reservation recipient,
	//  then decrypt to combine?
	return e.plaintext().WriteReservation(name, data)
}

func (e *encryptedFileOps) CombineParts(dst io.Writer, parts []string) (err error) {
	encWriter, err := age.Encrypt(dst, e.Recipients...)
	if err != nil {
		return trace.Wrap(err)
	}
	// No need to "defer encWriter.Close()" on failures.

	if err := combineParts(encWriter, parts, e.OpenFile, e.Logger); err != nil {
		return trace.ConvertSystemError(err)
	}

	// Flush last chunk.
	return trace.Wrap(encWriter.Close())
}

func (e *encryptedFileOps) plaintext() *plainFileOps {
	return &plainFileOps{
		Logger:   e.Logger,
		OpenFile: e.OpenFile,
	}
}

// combineParts is a shared implementation between [plainFileOps.CombineParts]
// and [encryptedFileOps.CombineParts].
//
// It does not wraps errors with trace.ConvertSystemError.
//
// Do not use this directly, use a [FileOps] implementation instead.
func combineParts(dst io.Writer, parts []string, openFile utils.OpenFileWithFlagsFunc, logger *slog.Logger) (err error) {
	for _, part := range parts {
		partFile, err := openFile(part, os.O_RDONLY, 0 /* perm */)
		if err != nil {
			return trace.Wrap(err)
		}
		if _, err := io.Copy(dst, partFile); err != nil {
			loggingClose(partFile, logger, "Failed to close part (io.Copy error flow)", "name", part)
			return trace.Wrap(err)
		}
		loggingClose(partFile, logger, "Failed to close part", "name", part)
	}
	return nil
}

func loggingClose(closer io.Closer, logger *slog.Logger, msg string, args ...any) {
	if err := closer.Close(); err != nil {
		logger.ErrorContext(context.Background(),
			msg,
			append(args, "error", err)...,
		)
	}
}
