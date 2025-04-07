---
authors: Alan Parra (alan.parra@goteleport.com), Erik Tate (erik.tate@goteleport.com) 
state: draft
---

# RFD 127 - Encrypted Session Recordings

## Required Approvers
* Engineering: @rosstimothy, @zmb3, @espadolini, @nklaassen
* Security: doyensec

## What

This document proposes an approach to encrypting session recording data before
writing to disk or any long term storage.

## Why

Recordings temporarily stored to disk can be easily tampered with by users with
enough access. This could even occur within the session being recorded if the
host user has root access.

Encrypting session recordings at rest can help prevent exposure of credentials
or other secrets that might be visible within the recording. 

## Details

This document should fulfill the following requirements:
- Ability to encrypt session recording data at rest in long term storage and
during any intermediate disk writes.
- Ability to replay encrypted sessions using the web UI.
- Ability to guard decryption using key material from an HSM or other supported
  keystore.
- Support for multiple auth servers using different HSM/KMS backends.
- An encryption algorithm suitable for this workload.

### Encryption Algorithm

This RFD assumes the usage of [age](https://github.com/FiloSottile/age), which
was chosen for its provenance, simplicity, and focus on strong cryptography
defaults without requiring customization. The formal spec can be found
[here](https://age-encryption.org/v1). Officially supported key algorithms are
limited to X25519 (recommended by the spec), Ed25519, and RSA. Support for
other algorithms would either have to be requested from the upstream or
manually implemented as a custom plugin.

### Config Changes

Encrypted session recording is a feature of the auth service and can be enabled
through the `session_recording_config` resource.
```yaml
# session_recording_config.yml
kind: session_recording_config
version: v2
spec:
  encrypted: true
```
HSM integration is facilitated through the existing configuration
options for setting up an HSM backed CA keystore through pkcs#11. Example
configuration found [here](https://goteleport.com/docs/admin-guides/deploy-a-cluster/hsm/#step-25-configure-teleport).

### Protobuf Changes
```proto
// api/proto/teleport/legacy/types/types.proto

// WrappedKey wraps a PrivateKey using an asymmetric keypair.
message WrappedKey {
  // WrappingPair is the asymmetric keypair used to wrap the private key.
  // Expected to be RSA
  SSHKeyPair WrappingPair = 1;
  bytes WrappedPrivateKey = 2 [
    (gogoproto.nullable) = true, // must be nullable
    (gogoproto.jsontag) = "wrapped_private_key"
  ];
  // The public key is included unencrypted to make it easier to find rotated
  // keypairs and to make available to proxy and host nodes.
  bytes PublicKey = 3 [(gogoproto.jsontag) = "public_key"];
  // Signals that a key should be rotated
  bool rotate = 4 [(gogoproto.jsontag) = "rotate"];
}

// SessionRecordingConfigStatusV2 contains all of the current and rotated keys
// used for encrypted session recording.
message SessionRecordingConfigStatusV2 {
  // ActiveKeys is a list of active, wrapped X25519 private keys. There should
  // be at most one wrapped key per auth server using the
  // SessionRecordingConfigV2 resource unless keys are being rotated.
  repeated WrappedKey ActiveKeys = 2 [
    (gogoproto.jsontag) = "active_keys"
  ];
  // RotatedKeys is a list of wrapped private keys that have been rotated.
  // These are kept to decrypt historical encrypted session recordings.
  repeated WrappedKey RotatedKeys = 3 [
    (gogoproto.jsontag) = "rotated_keys"
  ];
}

// SessionRecordingConfigSpecV2 is the actual data we care about
// for SessionRecordingConfig.
message SessionRecordingConfigSpecV2 {
  // existing fields omitted 

  BoolValue encrypted = 3 [(gogoproto.jsontag) = "encrypted"]
}

// SessionRecordingConfigV2 contains session recording configuration.
message SessionRecordingConfigV2 {
  // existing fields omitted

  // Status contains all of the current and rotated keys used for encrypted
  // session recording
  SessionRecordingConfigStatusV2 Status = 6 [
    (gogoproto.nullable) = true,
    (gogoproto.jsontag) = "status"
  ]
}

// api/proto/teleport/trust/v1/trust_service.proto

// TrustService provides methods to manage certificate authorities.
service TrustService {
  // existing RPCs omitted

  // RotateSessionRecordingKeys rotates the keys associated with encrypting and
  // decrypting session recordings.
  rpc RotateSessionRecordingKeys(RotateSessionRecordingKeysRequest) returns (RotateSessionRecordingKeysResponse);
}

// Request for RotateSessionRecordingKeys.
message RotateSessionRecordingKeysRequest {
  // KeyType defines which key type should be rotated. Valid options are:
  // "data" and "wrap"
  string KeyType = 1;
}

// Response for RotateSessionRecordingKeys
message RotateSessionRecordingKeysResponse {}
```

### Session Recording Modes

There are four session recording modes that describe where the recordings are
captured and how they're shipped to long term storage.
- `proxy-sync`
- `proxy`
- `node-sync`
- `node`
Where the recordings are collected is largely unimportant to the encryption
strategy, but whether or not they are handled async or sync has different
requirements.

In sync modes the session recording data is written immediately to long term
storage without intermediate disk writes. This means we can simply instantiate
an age encryptor at the start of the session and encrypt the recording data as
it's sent to long term storage.

In async modes the session recording data is written to intermediate `.part`
files. These files are collected until they're ready for upload and are then
combined into a single `.tar` file. In order to encrypt individual parts, we
will build a special `io.Writer` that contains a single instance of the age
encryptor that can proxy writes across multiple files. This will require
maintaining a concurrency-safe mapping of in-progress uploads to encrypted
writers which incurs a bit of added complexity. However it intentionally avoids
any sort of intermediate key management which seems a worthwhile tradeoff.

### Protocols

We record sessions for multiple protocols, including ssh, k8s, database
sessions and more. Because this approach encrypts at the point of writing
without modifying the recording structure, the strategy for encryption is
expected to be the same across all protocols.

### Encryption

At a high level, `age` encryption works by generating a per-file symmetric key
used for data encryption. That key is wrapped using an asymmetric keypair and
included in the header section of the encrypted file as a stanza. Plugins
implementing different key algorithms only affect the crypto involved with
wrapping and unwrapping data encryption keys.

In both proxy and node recording modes, the public `X25519` key used for
wrapping `age` data keys will come from
`session_recording_config.status.active_keys`. All unique public keys present
will be added as recipients.

### Key Types

This design relies on a few different types of keys.
- File keys generated by `age`. These are data encryption keys used during
  symmetric encryption of the recording data.
- `Identity` and `Recipient` keys used by `age` to wrap and unwrap file keys.
  These are an asymmetric `X25519` keypair.
- HSM/KMS-owned `RSA` keypairs used to wrap the `Identity` key. These allow the
  HSM or KMS keystore to "own" decryption without requiring proxy or host nodes
  to make encryption requests to the auth server.

### Key Generation

To simplify integration with different HSM/KMS systems, the keypair used for 
data encryption through `age` will be a software generated `X25519` keypair.
For the rest of this section I will refer to the public key as the `Recipient`
and the private key as the `Identity` in keeping with `age` terminology.

The auth server generating the keys will then use the configured CA keystore to
generate an `RSA` keypair used to encrypt, or wrap, the `Identity`. Once
encrypted, the `Recipient`, wrapped `Identity`, and wrapping keypair are added
as a new entry to the `session_recording_config.status.active_keys` list.

If a new auth server is added to the environment, it will find that there is
already a configured `X25519` keypair in the session recording config. It will
check if any active keys are accessible (detailed in the next paragraph) and,
in the case that there are none, generate a new `RSA` wrapping keypair. The new
keypair  will be added as an entry to the list of active keys but without a
wrapped `Identity`. Any other auth server with an active key can inspect the
new entry, unwrap their own copy of the `Identity`, and wrap it again using
software encryption and the included public `RSA` key provided by the new auth
server. The re-wrapped `Identity` is then saved as the wrapped key for the new
entry and both auth servers will be able to decrypt sessions.

When using KMS keystores, auth servers may share access to the same key. In that
case, they will also share the same wrapped key which can be identified by the
key ID. For HSMs integrated over PKCS#11, the auth server's host UUID is
attached to the key ID and will be used to determine access.

### Decryption and Replay

Because decryption will happen in the auth service before streaming to the
client, the UX of replaying encrypted recordings is no different from
unencrypted recordings. The auth server will find and unwrap its active key in
the `session_recording_config` using either the key's identifier within the KMS
or the `host_uuid` attached to HSM derived keys. It will use that key to
decrypt the data on the fly as it's streamed back to the client. This should be
compatible with all replay clients, including web.

If a recording was encrypted with a rotated key, the auth server will also need
to search the list of rotated keys to find and unwrap the correct key. Public
keys are included with their rotated private keys in order to facilitate faster
searching.

### Key Rotation

The `X25519` keypair used with `age` and the keystore backed wrapping keys
may need to be rotated. Both cases are supported by making rotation requests to
the auth service and can be fulfilled by any auth server with an active wrapped
key.

When an auth server receives a rotation request for wrapped keys, it will:
- Unwrap its copy of the active `X25519` private key and generate a new `RSA`
  wrapping keypair.
- Wrap the `X25519` private key with the new `RSA` public key and replace its
  active key.
- Mark all other active keys for rotation by setting their `rotate` field to
  `true`. This is done in place of removing all other keys in order to avoid
  any situation where an auth server initiates a rotation and then crashes,
  leaving all other auth servers without a way of retrieving new keys.

All other auth servers will find their keys marked for rotation and go through
the same rotation process omitting the step of marking other keys.

When an auth server receives a rotation request for the `X25519` keypair, it
will:
- Copy all wrapped keys from the list of active keys to the list of rotated
  keys.
- Generate a new `X25519` keypair.
- Iterate over all active wrapped keys and use their public `RSA` keys to
  replace their `wrapped_private_key` value using software encryption.

These are two separate operations and should be handled separately in order to
avoid any period of time where valid keys are unavailable. For simplicity,
auth servers without immediate access to an active key will return an error to
the client letting them know the rotation should be tried again.

#### `tctl` Changes

Key rotation will be handled through `tctl` using a new subcommand:
```bash
tctl auth rotate recordings --type=data # for rotating X25519 pairs
tctl auth rotate recordings --type=wrap # for rotating HSM/KMS backed wrapping keys
```
The reasoning for the new subcommand was to try and avoid cases where you might
forget the `--type` flag and initiate a rotation of the user and host CAs.

### Security

Protection of key material invovled with encrypting session recordings is
largely managed by our existing keystore features. The one exception being the
private keys used by `age` to decrypt files during playback. Whenever possible,
those keys will be wrapped by the backing keystore such that decryption related
secrets are never directly accessible.

One of the primary concerns outside of key management is ensuring that session
recording data is always encrypted before landing on disk or in long term
storage. In order to help enforce this, all session recording interactions
should be gated behind a standard interface that can be implemented as either
plaintext or encrypted. This will help ensure that once the encrypted writer
has been selected, any interactions with session recordings are encrypted by
default.

## UX Examples

For the most part, the user experience of encrypted session recordings is
identical to non-encrypted session recordings. The only notable change is the
addition of the `tctl auth rotate recordings` subcommand for rotating keys
related to encrypted session recording.

### Teleport admin rotating `Identity` and `Recipient` keypair
```bash
tctl auth rotate recordings --type=data
```

### Teleport admin rotating wrapping keys
```bash
tctl auth rotate recordings --type=wrap
```

### Teleport admin replaying encrypted session using tsh
```bash
tsh play 49608fad-7fe3-44a7-b3b5-fab0e0bd34d1
```

### Test Plan
- Sessions are recorded when `auth_service.session_recording_encryption: on`.
- Encrypted sessions can be played back in both web and `tsh`.
- Encrypted sessions can be recorded and played back with or without a backing
- Key rotations for both key types don't break new recordings or remove the
  ability to decrypt old recordings.
