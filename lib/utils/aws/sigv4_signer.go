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

package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/gravitational/trace"
)

// Signer applies AWS v4 signing to given request.
type Signer interface {
	// Sign signs AWS v4 requests with the provided body, service name, region the
	// request is made to, and time the request is signed at.
	Sign(ctx context.Context, r *http.Request, body []byte, service, region string, signTime time.Time) error
}

type sigv4Signer struct {
	signer              *v4.Signer
	credentialsProvider aws.CredentialsProvider
}

// NewSigner creates a new V4 signer.
func NewSigner(credentialsProvider aws.CredentialsProvider, signingServiceName string) *sigv4Signer {
	options := func(o *v4.SignerOptions) {
		// s3 and s3control requests are signed with URL unescaped (found by
		// searching "DisableURIPathEscaping" in "aws-sdk-go/service"). Both
		// services use "s3" as signing name. See description of
		// "DisableURIPathEscaping" for more details.
		if signingServiceName == "s3" {
			o.DisableURIPathEscaping = true
		}
	}
	return &sigv4Signer{
		signer:              v4.NewSigner(options),
		credentialsProvider: credentialsProvider,
	}
}

// Sign signs AWS v4 requests with the provided body, service name, region the
// request is made to, and time the request is signed at.
func (s *sigv4Signer) Sign(ctx context.Context, r *http.Request, body []byte, service, region string, signTime time.Time) error {
	creds, err := s.credentialsProvider.Retrieve(ctx)
	if err != nil {
		return trace.Wrap(err)
	}

	var payloadHash string
	if r.Body == nil || len(body) == 0 {
		payloadHash = emptyPayloadHash
	} else {
		sum := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(sum[:])
	}

	return trace.Wrap(s.signer.SignHTTP(ctx, creds, r, payloadHash, service, region, signTime))
}

const emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
