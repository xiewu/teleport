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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/go-cmp/cmp"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"

	apievents "github.com/gravitational/teleport/api/types/events"
	"github.com/gravitational/teleport/lib/events"
	"github.com/gravitational/teleport/lib/session"
)

func TestNonModifyingUpload_plaintext(t *testing.T) {
	t.Parallel()

	t.Run("plaintext", func(t *testing.T) {
		t.Parallel()

		nopDecrypter := func(src io.Reader) (io.Reader, error) { return src, nil }
		runTestNonModifyingUpload(t, testNonModifyingUploadOpts{
			Decrypter: nopDecrypter,
		})
	})

	t.Run("encrypted", func(t *testing.T) {
		t.Parallel()

		const (
			recipient1 = `age1488nwl034sc9h3m9kn5akwuq54gd3mf3aqv0cnkutdw2vqzk8vgse2qphm`
			recipient2 = `age13k6u7udypf2cd2kg2yww9a0xenvuudk0z60g9hlnknen802czdpqxjdnzd`
			identity2  = `AGE-SECRET-KEY-1KAQGEVFVNV435Q8DM8QJWLD05XFPE6JGACYARJN4XPQP077Q4X8SXRASXU`
		)

		var recipients []age.Recipient
		for _, r := range []string{recipient1, recipient2} {
			parsed, err := age.ParseX25519Recipient(r)
			require.NoError(t, err, "age.ParseX25519Recipient()")
			recipients = append(recipients, parsed)
		}

		id2, err := age.ParseX25519Identity(identity2)
		require.NoError(t, err, "age.ParseX25519Identity()")

		decrypter := func(src io.Reader) (io.Reader, error) { return age.Decrypt(src, id2) }
		runTestNonModifyingUpload(t, testNonModifyingUploadOpts{
			Recipients: recipients,
			Decrypter:  decrypter,
		})
	})
}

type testNonModifyingUploadOpts struct {
	// Recipients are the recipients used for the local streamer.
	Recipients []age.Recipient
	// Decrypter for one of the recipients. Must not be nil.
	Decrypter func(io.Reader) (io.Reader, error)
}

func runTestNonModifyingUpload(t *testing.T, opts testNonModifyingUploadOpts) {
	rootDir := t.TempDir()
	recordsDir := filepath.Join(rootDir, "records")
	streamingDir := filepath.Join(rootDir, "streaming")
	corruptedDir := filepath.Join(rootDir, "corrupted")

	localStreamer, err := NewStreamer(ProtoStreamerConfig{
		Dir:        streamingDir,
		Recipients: opts.Recipients,
	})
	require.NoError(t, err, "NewStreamer(local)")

	ctx := t.Context()

	// const for simplicity.
	const sessionID session.ID = "37fee8b5-a8a7-4e81-b2b7-ca62b6833cac"
	allEvents := []apievents.AuditEvent{
		&apievents.SessionStart{
			Metadata: apievents.Metadata{
				Type: events.SessionStartEvent,
				Code: events.SessionStartCode,
			},
		},
		&apievents.SessionPrint{
			Metadata: apievents.Metadata{
				Type:  events.SessionPrintEvent,
				Index: 1, // Must be > the last index, otherwise it's skipped!
			},
			ChunkIndex:        0,
			Data:              []byte("Hello, World."),
			Bytes:             13, // yes, this is the length of the Data slice.
			DelayMilliseconds: 1,
			Offset:            0,
		},
		&apievents.SessionEnd{
			Metadata: apievents.Metadata{
				Type:  events.SessionEndEvent,
				Code:  events.SessionEndCode,
				Index: 2,
			},
		},
		&apievents.SessionLeave{
			Metadata: apievents.Metadata{
				Type:  events.SessionLeaveEvent,
				Code:  events.SessionLeaveCode,
				Index: 3,
			},
		},
	}

	// Step 1: stream events to localStreamer.
	// In practice this happens as part of an SSH session.
	require.NoError(t,
		streamEvents(ctx, localStreamer, sessionID, allEvents),
		"Streaming session recording events to localStreamer",
	)

	var streamedBytes []byte // streamed ".tar" file contents

	// Capture the streamed file before the Uploader starts, otherwise we have
	// to race it and the file could be deleted.
	{
		wantFile := filepath.Join(streamingDir, string(sessionID)+".tar")
		streamedBytes, err = os.ReadFile(wantFile)
		require.NoError(t, err, streamedBytes)
		// Sanity check.
		require.NotEmpty(t,
			streamedBytes,
			"Streaming file empty. This is unexpected.",
		)
	}

	// Step 1.1: validate streaming/ session file.
	decrypter := opts.Decrypter
	dec, err := decrypter(bytes.NewReader(streamedBytes))
	require.NoError(t, err, "decrypter()")
	validateSessionRecording(ctx, t, dec, allEvents)

	remoteStreamer, err := NewStreamer(ProtoStreamerConfig{
		Dir: recordsDir,
		// Notice no encryption Recipients!
	})
	require.NoError(t, err, "NewStreamer(remote)")

	clock := clockwork.NewRealClock()
	const scanPeriod = 100 * time.Millisecond
	uploader, err := NewUploader(UploaderConfig{
		ScanDir:           streamingDir,
		CorruptedDir:      corruptedDir,
		Clock:             clock,
		ScanPeriod:        scanPeriod,
		ConcurrentUploads: 1,
		Streamer:          remoteStreamer,
	})
	require.NoError(t, err, "NewUploader()")
	t.Cleanup(uploader.Close)

	uploaderDone := make(chan struct{})
	go func() {
		defer close(uploaderDone)
		assert.NoError(t,
			uploader.Serve(ctx),
			"uploader.Serve()")
	}()
	t.Cleanup(func() {
		const maxWait = 2 * time.Second // arbitrary
		select {
		case <-uploaderDone:
		case <-time.After(maxWait):
			t.Error("Timed out waiting for the uploader goroutine to exit")
		}
	})

	// Step 2: the Uploader should pick up the session recording in the streaming/
	// folder and, through a very roundabout process, move it to the records/
	// folder.

	// Wait for the recording under records/
	var recordsBytes []byte // records/ "tar" file contents.
	{
		wantFile := filepath.Join(recordsDir, string(sessionID)+".tar")

		const maxWait = 5 * time.Second // arbitrary
		assert.Eventually(t,
			func() bool {
				var err error
				recordsBytes, err = os.ReadFile(wantFile)
				if err != nil && !os.IsNotExist(err) {
					t.Errorf("os.ReadFile() returned unexpected error: %v", err)
				}
				return err == nil
			},
			maxWait, scanPeriod,
			`Timed out waiting for "uploaded" records/ session file`,
		)

		// Sanity check.
		require.NotEmpty(t, recordsBytes, "Records file empty. This is unexpected.")
	}

	// Step 2.1: did we get the exact same file as before?
	assert.Equal(t, streamedBytes, recordsBytes, "streaming/ and records/ session files are not byte-by-byte equal")

	// Step 2.2: Sanity check. Completely redundant.
	dec, err = decrypter(bytes.NewReader(recordsBytes))
	require.NoError(t, err, "decrypter()")
	validateSessionRecording(ctx, t, dec, allEvents)
}

func streamEvents(
	ctx context.Context,
	localStreamer *events.ProtoStreamer,
	sessionID session.ID,
	allEvents []apievents.AuditEvent,
) (err error) {
	auditStream, err := localStreamer.CreateAuditStream(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("create audit stream: %w", err)
	}
	defer func() {
		if auditStream == nil { // nil means already closed.
			return
		}
		if err2 := auditStream.Close(context.Background()); err2 != nil {
			err = errors.Join(
				err,
				fmt.Errorf("close audit stream: %w", err2),
			)
		}
	}()

	// Consume the stream's channels.
	// Unclear if this is necessary.
	streamGoroutineDone := make(chan struct{})
	go func() {
		defer close(streamGoroutineDone)
		for {
			select {
			case <-auditStream.Done():
				return
			case status := <-auditStream.Status():
				// Note: we could capture the UploadID from here, if we wanted.
				_ = status
			}
		}
	}()
	defer func() {
		const maxWait = 1 * time.Second // arbitrary
		select {
		case <-streamGoroutineDone:
		case <-time.After(maxWait):
			err = errors.Join(
				err,
				errors.New("timed out waiting for stream status goroutine to exit"),
			)
		}
	}()

	preparer := events.NoOpPreparer{}
	for _, e := range allEvents {
		prepared, err := preparer.PrepareSessionEvent(e)
		if err != nil {
			return fmt.Errorf("prepare session event: %w", err)
		}
		if err := auditStream.RecordEvent(ctx, prepared); err != nil {
			return fmt.Errorf("record event: %w", err)
		}
	}

	if err := auditStream.Complete(ctx); err != nil {
		return fmt.Errorf("complete audit stream: %w", err)
	}

	err = auditStream.Close(ctx)
	auditStream = nil // Avoid double-close. The code seems unprepared.
	if err != nil {
		return fmt.Errorf("close audit stream: %w", err)
	}

	return nil
}

func validateSessionRecording(
	ctx context.Context,
	t *testing.T,
	src io.Reader,
	want []apievents.AuditEvent,
) {
	t.Helper()

	got, err := events.
		NewProtoReader(src).
		ReadAll(ctx)
	require.NoError(t, err, "ProtoReader.ReadAll()")

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Fatalf("Session events mismatch (-want +got)\n%s", diff)
	}
}
