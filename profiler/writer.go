package profiler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/pprof/profile"
)

type FileProfileWriter struct {
	dir string

	profileBufferPool sync.Pool
}

func NewFileWriter(dirPath string) *FileProfileWriter {
	return &FileProfileWriter{
		dir: dirPath,
		profileBufferPool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(nil)
			},
		},
	}
}

func (fw *FileProfileWriter) Write(ctx context.Context, labels map[string]string, prof *profile.Profile) error {
	var path string
	path += labels["__name__"]
	path += fmt.Sprintf("_%s", labels["node"])
	path += fmt.Sprintf("_%s", labels["pid"])
	path += fmt.Sprintf("_%03d.pb.gz", time.Now().UnixNano())

	if err := os.MkdirAll(fw.dir, 0755); err != nil {
		return fmt.Errorf("could not use temp dir ", fw.dir, ": ", err.Error())
	}

	f, err := os.OpenFile(filepath.Join(fw.dir, path), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return err
	}
	if err := prof.Write(f); err != nil {
		return err
	}

	return nil
}
