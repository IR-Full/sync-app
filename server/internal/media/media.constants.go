package media

import "errors"

// ErrExists means the object already exists. Uploads are CREATE-ONLY: a signed
// upload URL stays valid for its whole TTL, so without this a holder could keep
// replacing the bytes behind a media_ref that recipients have already been told
// about — swapping content under a message after the fact.
var ErrExists = errors.New("media: object already exists")

// eicar is the EICAR test signature (industry-standard benign AV test file).
var eicar = []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)
