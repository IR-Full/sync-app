package id

// Epoch is the custom epoch: 2024-01-01T00:00:00Z in unix millis.
const Epoch int64 = 1704067200000

const (
	nodeBits = 10
	seqBits  = 12

	maxNode   = -1 ^ (-1 << nodeBits) // 1023
	maxSeq    = -1 ^ (-1 << seqBits)  // 4095
	timeShift = nodeBits + seqBits
	nodeShift = seqBits
)
