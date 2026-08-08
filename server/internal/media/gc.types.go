package media

import "context"

// Referencer answers whether a media ref is still reachable from a live message.
type Referencer interface {
	MediaRefExists(ctx context.Context, ref string) (bool, error)
}
