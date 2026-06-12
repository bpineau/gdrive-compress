package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

type imgFile struct {
	ID           string
	Name         string
	Size         int64
	ModifiedTime string
}

var errLimitReached = errors.New("limit reached")

func listImages(ctx context.Context, srv *drive.Service, minSize int64, processed map[string]bool, limit int) ([]imgFile, error) {
	var out []imgFile
	err := srv.Files.List().
		Q("mimeType='image/jpeg' and trashed=false").
		Fields("nextPageToken, files(id, name, size, modifiedTime)").
		PageSize(1000).
		Spaces("drive").
		Pages(ctx, func(res *drive.FileList) error {
			for _, f := range res.Files {
				if f.Size < minSize || processed[f.Id] {
					continue
				}
				out = append(out, imgFile{
					ID:           f.Id,
					Name:         f.Name,
					Size:         f.Size,
					ModifiedTime: f.ModifiedTime,
				})
				if limit > 0 && len(out) >= limit {
					return errLimitReached
				}
			}
			return nil
		})
	if err != nil && !errors.Is(err, errLimitReached) {
		return nil, fmt.Errorf("files.list: %w", err)
	}
	return out, nil
}

func download(ctx context.Context, srv *drive.Service, fileID string) ([]byte, error) {
	var data []byte
	err := withRetry(ctx, func() error {
		res, err := srv.Files.Get(fileID).Context(ctx).Download()
		if err != nil {
			return err
		}
		defer res.Body.Close()
		data, err = io.ReadAll(res.Body)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	return data, nil
}

// replaceContent updates the file content in place, preserving the original
// modifiedTime so Drive's "Recent" / sort-by-date isn't disturbed.
func replaceContent(ctx context.Context, srv *drive.Service, fileID, mimeType, origModifiedTime string, content []byte) error {
	meta := &drive.File{MimeType: mimeType}
	if origModifiedTime != "" {
		meta.ModifiedTime = origModifiedTime
	}
	return withRetry(ctx, func() error {
		_, err := srv.Files.Update(fileID, meta).
			Media(bytes.NewReader(content), googleapi.ContentType(mimeType)).
			Context(ctx).
			Do()
		return err
	})
}

// withRetry runs op, retrying transient 429/5xx/network failures with
// exponential backoff, up to 5 attempts.
func withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for attempt := range 5 {
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !shouldRetry(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(1<<attempt) * time.Second):
		}
	}
	return fmt.Errorf("after retries: %w", lastErr)
}

func shouldRetry(err error) bool {
	if ge, ok := errors.AsType[*googleapi.Error](err); ok {
		return ge.Code == 429 || ge.Code >= 500
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "eof")
}
