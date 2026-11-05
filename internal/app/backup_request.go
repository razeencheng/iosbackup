package app

import (
	"errors"
	"io"
	"iosbackup/internal/i18n"
	"net/http"
)

type backupRequest struct {
	Mode string `json:"mode"`
}

func (r backupRequest) normalized() (backupRequest, error) {
	if r.Mode == "" {
		r.Mode = "auto"
	}
	if r.Mode != "auto" && r.Mode != "full" {
		return r, i18n.NewError(i18n.InvalidBackupMode, nil)
	}
	return r, nil
}

func readBackupRequest(w http.ResponseWriter, r *http.Request) (backupRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var request *backupRequest
	err := decodeRequestJSON(r, &request)
	if errors.Is(err, io.EOF) {
		return backupRequest{Mode: "auto"}, nil
	}
	if err != nil {
		return backupRequest{}, err
	}
	if request == nil {
		return backupRequest{}, errors.New("backup request must be an object")
	}
	return request.normalized()
}
