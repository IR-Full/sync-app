package wire

import (
	"errors"

	"github.com/klauspost/compress/zstd"
)

// zstd with a shared dictionary compresses short, repetitive chat frames much
// better than gzip: gzip must relearn common patterns every frame, while a
// dictionary primes the compressor with words/JSON-keys/protobuf field tags that
// recur across messages. The dictionary below is a small hand-seeded content
// dictionary; a production dictionary is trained from a real message corpus with
//
//	zstd --train samples/* -o SyncApp.dict
//
// and swapped in via sharedDict — the wire format is unchanged (FlagZstd).

var (
	zstdEnc = mustEncoder()
	zstdDec = mustDecoder()
)

func mustEncoder() *zstd.Encoder {
	// Raw content dictionary (not a trained .dict); primes the window with common
	// tokens. A trained dictionary drops in via the same API with a real id.
	e, err := zstd.NewWriter(nil,
		zstd.WithEncoderDictRaw(dictID, sharedDict),
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil { // fall back to no-dict rather than crash
		e, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	}
	return e
}

func mustDecoder() *zstd.Decoder {
	d, err := zstd.NewReader(nil, append(decoderLimits(),
		zstd.WithDecoderDictRaw(dictID, sharedDict),
		zstd.WithDecoderConcurrency(0),
	)...)
	if err != nil {
		d, _ = zstd.NewReader(nil, decoderLimits()...)
	}
	return d
}

// decoderLimits bound what one frame may expand to. Without them DecodeAll
// allocates the WHOLE output before the length check after it runs: a 112 KB
// frame of compressed zeros cost 1 GiB, and a 16 MiB one would take the process
// down — before authentication, since the flag is read from the frame itself.
// MaxMemory stops decoding once the output would pass MaxPayloadSize; MaxWindow
// stops a frame from demanding a huge back-reference buffer up front.
func decoderLimits() []zstd.DOption {
	return []zstd.DOption{
		zstd.WithDecoderMaxMemory(MaxPayloadSize),
		zstd.WithDecoderMaxWindow(MaxPayloadSize),
	}
}

// zstdCompress compresses with the shared dictionary. Encoder.EncodeAll is safe
// for concurrent use.
func zstdCompress(b []byte) []byte {
	return zstdEnc.EncodeAll(b, nil)
}

// zstdDecompress decompresses, bounding output against zip bombs.
func zstdDecompress(b []byte) ([]byte, error) {
	out, err := zstdDec.DecodeAll(b, nil)
	if errors.Is(err, zstd.ErrDecoderSizeExceeded) || errors.Is(err, zstd.ErrWindowSizeExceeded) {
		return nil, ErrTooLarge
	}
	if err != nil {
		return nil, err
	}
	if len(out) > MaxPayloadSize {
		return nil, ErrTooLarge
	}
	return out, nil
}
