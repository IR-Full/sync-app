package wire

import "encoding/base64"

/*
Reading and writing the two shapes of a secret message payload.

SecretMsgBody carries the ratchet header and ciphertext twice over: once in the
legacy text fields, once as raw bytes. Only one is ever populated, and which one
is a property of the connection rather than of the message — see the field
comments.

The two halves of the LEGACY form are encoded differently, and that asymmetry is
easy to get wrong because the field comment used to claim otherwise. What the
clients actually do:

  - ratchet_header is TEXT, not base64. Every client puts a JSON object there —
    the X3DH bootstrap on the first message, the ratchet header afterwards. Look
    at `openSecretMessage` in the web client: it calls JSON.parse on the field.
  - ciphertext IS base64, because it is genuinely opaque bytes.

So converting to the binary form is `[]byte(header)` for one and a base64 decode
for the other. Treating the header as base64 would reject every real message,
which is exactly what the integration suite caught when this was first written the
other way.

These four functions are the only place that has to know any of it.
*/

// SecretPayload returns the ratchet header and ciphertext as bytes, whichever
// form they arrived in.
//
// The binary fields win when both are set. That is not arbitrary: a peer that
// sends both is either confused or trying to make two payloads look like one
// message, and the binary form is the one a current client means.
//
// ok is false only when there is no payload at all, or when the ciphertext is not
// decodable. A malformed message is rejected at the boundary rather than stored
// and relayed, so a peer cannot use the queue to hold bytes nobody can open.
func SecretPayload(b SecretMsgBody) (header, cipher []byte, ok bool) {
	if len(b.Header) > 0 || len(b.Cipher) > 0 {
		return b.Header, b.Cipher, true
	}
	if b.RatchetHeader == "" && b.Ciphertext == "" {
		return nil, nil, false
	}
	// The header is text (JSON), so its bytes are its bytes. No decode.
	header = []byte(b.RatchetHeader)
	c, err := base64.StdEncoding.DecodeString(b.Ciphertext)
	if err != nil {
		return nil, nil, false
	}
	return header, c, true
}

// SetSecretPayloadBinary fills the raw fields and clears the text ones.
//
// Clearing matters. Leaving both populated would send the payload twice, which
// turns a change made to SAVE 33% into one that costs 100% — the exact opposite
// of the point.
func SetSecretPayloadBinary(b *SecretMsgBody, header, cipher []byte) {
	b.Header, b.Cipher = header, cipher
	b.RatchetHeader, b.Ciphertext = "", ""
}

// SetSecretPayloadLegacy fills the text fields and clears the raw ones, for peers
// that did not negotiate the binary form.
//
// The header goes back verbatim as text and the ciphertext is re-encoded, mirroring
// SecretPayload. Round-tripping through here is lossless for both.
func SetSecretPayloadLegacy(b *SecretMsgBody, header, cipher []byte) {
	b.RatchetHeader = string(header)
	b.Ciphertext = base64.StdEncoding.EncodeToString(cipher)
	b.Header, b.Cipher = nil, nil
}

// SetSecretPayloadFor picks the encoding a peer can read.
//
// CapSecretQueue is the discriminator, and it is the right one for a reason worth
// stating: it means "this client speaks the durable secret-chat protocol", which
// is a capability that shipped together with the binary payload in the same client
// release. A separate bit for the encoding would have been more precise and would
// have described a client that never existed.
func SetSecretPayloadFor(b *SecretMsgBody, caps Cap, header, cipher []byte) {
	if caps&CapSecretQueue != 0 {
		SetSecretPayloadBinary(b, header, cipher)
		return
	}
	SetSecretPayloadLegacy(b, header, cipher)
}
