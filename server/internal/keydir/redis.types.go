package keydir

import (
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// redisDir is the shared, multi-node key directory. Per device it stores a hash
// keydir:b:<user>:<device> (identity/signing/spk/sig), a list
// keydir:otp:<user>:<device> of one-time prekeys (LPOP on fetch), and a set
// keydir:dev:<user> of the user's device ids. Only public keys are stored, so a
// leak never enables decryption.
type redisDir struct {
	rdb *redis.Client
	log *slog.Logger
}
