package quota

import (
	"errors"
	"io"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	lr := io.LimitReader(r, limit+1)
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("response body too large")
	}
	return b, nil
}
