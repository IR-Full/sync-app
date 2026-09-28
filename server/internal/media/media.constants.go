package media

import "errors"

// ErrExists means the object already exists. Uploads are CREATE-ONLY: a signed
// upload URL stays valid for its whole TTL, so without this a holder could keep
// replacing the bytes behind a media_ref that recipients have already been told
// about — swapping content under a message after the fact.
var ErrExists = errors.New("media: object already exists")

// eicar is the EICAR test signature (industry-standard benign AV test file).
var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)

// defaultMaxSize is the deployment ceiling when an operator sets none.
//
// It matches the largest tier rather than the smallest, because per-account
// limits now come from entitlements and this value only has to be big enough not
// to contradict them. It was 100 MiB, which silently overrode every tier above
// the free one.
const defaultMaxSize int64 = 4 << 30 // 4 GiB

// ErrTooLargeForTier means the caller's PLAN refused the upload, not the
// deployment. Its own error so the gateway can offer an upgrade rather than
// report a flat refusal for something that is purchasable.
var ErrTooLargeForTier = errors.New("media: file is larger than your plan allows")
