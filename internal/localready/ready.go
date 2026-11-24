package localready

import (
	"errors"
	"fmt"
	"os"
)

// Open accepts the dedicated unnamed pipe passed by the installed service.
func Open() (*os.File, error) {
	file := os.NewFile(3, "service readiness")
	if file == nil {
		return nil, errors.New("readiness pipe unavailable")
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		file.Close()
		return nil, errors.New("readiness pipe unavailable")
	}
	return file, nil
}

func Notify(pipe *os.File, message string) error {
	if pipe == nil {
		return nil
	}
	_, err := fmt.Fprintln(pipe, message)
	return errors.Join(err, pipe.Close())
}
