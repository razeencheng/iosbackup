package app

import (
	"bytes"
	"context"
	"encoding/json"
	"iosbackup/internal/backupstore"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func historyHTTPFixture(t *testing.T) (*application, http.Handler, backupstore.Point) {
	t.Helper()
	a := historyTestApp(t)
	historyCommand(t, a, "finished", "export me", 0, nil)
	if e := a.PerformBackup("CONTROL"); e != nil {
		t.Fatal(e)
	}
	if e := a.initializeSecurity(); e != nil {
		t.Fatal(e)
	}
	var p backupstore.Point
	for _, v := range historyPoints(t, a) {
		p = v
	}
	t.Cleanup(a.closeHistoryHTTP)
	return a, a.setupRoutes(), p
}
func historyRequest(t *testing.T, a *application, h http.Handler, method, url string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, url, bytes.NewReader(data))
	r.Header.Set(csrfHeader, a.csrfManager.Token())
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestBackupHistoryHTTP(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	base := "/api/backup-point/" + p.ID
	w := historyRequest(t, a, h, "GET", "/api/backup-history/CONTROL", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), p.ID) {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	for _, secret := range []string{"credential_ref", a.paths.BackupsRoot, "/objects/"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("private storage exposed")
		}
	}
	for _, tc := range []struct {
		method, url string
		body        any
		want        int
	}{
		{"POST", base, nil, 405}, {"GET", "/api/backup-point/not-an-id", nil, 404}, {"GET", base + "?udid=OTHER", nil, 404},
		{"POST", base + "/pin", map[string]any{"pinned": true, "revision": 0}, 409},
		{"DELETE", base, map[string]any{"revision": 2}, 400},
		{"POST", base + "/pin", map[string]any{"pinned": true, "revision": 2, "path": "/etc/passwd"}, 400},
	} {
		w = historyRequest(t, a, h, tc.method, tc.url, tc.body)
		if w.Code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.url, w.Code, w.Body)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", base+"/pin", strings.NewReader(`{"pinned":true,"revision":2}`)))
	if w.Code != 403 {
		t.Fatalf("csrf %d", w.Code)
	}
	a.mu.Lock()
	a.devices["CONTROL"].IsOnline = false
	a.mu.Unlock()
	w = historyRequest(t, a, h, "GET", base, nil)
	if w.Code != 200 {
		t.Fatal("offline source not readable", w.Code)
	}
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	w = historyRequest(t, a, h, "POST", base+"/pin", map[string]any{"pinned": true, "revision": c.Revision})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	c, _ = s.Snapshot()
	if !c.Points[p.ID].Pinned {
		t.Fatal("pin not persisted")
	}
	lease, e := s.Acquire(p.ID)
	if e != nil {
		t.Fatal(e)
	}
	w = historyRequest(t, a, h, "DELETE", base, map[string]any{"revision": c.Revision, "confirm": p.ID})
	if w.Code != 409 {
		t.Fatal("busy deletion", w.Code)
	}
	lease.Close()
	w = historyRequest(t, a, h, "DELETE", base, map[string]any{"revision": c.Revision, "confirm": p.ID})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if len(historyPoints(t, a)) != 0 {
		t.Fatal("not deleted")
	}
}
func TestBackupHistoryHTTPPolicy(t *testing.T) {
	a, h, _ := historyHTTPFixture(t)
	body := map[string]any{"policy": backupstore.Policy{Enabled: true, Days: 1}}
	w := historyRequest(t, a, h, "PUT", "/api/backup-history/CONTROL/policy", body)
	if w.Code != 409 {
		t.Fatal("unpreviewed change", w.Code)
	}
	w = historyRequest(t, a, h, "POST", "/api/backup-history/CONTROL/policy-preview", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var response struct {
		Token string `json:"token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	body["token"] = response.Token
	w = historyRequest(t, a, h, "PUT", "/api/backup-history/CONTROL/policy", body)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body)
	}
	service := a.historyService()
	service.mu.Lock()
	done := service.policies["CONTROL"].done
	service.mu.Unlock()
	<-done
	if a.configs["CONTROL"].History.Days != 1 {
		t.Fatal("policy not saved")
	}
	w = historyRequest(t, a, h, "PUT", "/api/backup-history/CONTROL/policy", body)
	if w.Code != 409 {
		t.Fatal("reused preview", w.Code)
	}
}
func TestBackupExportHTTP(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	w := historyRequest(t, a, h, "POST", "/api/backup-point/"+p.ID+"/exports", map[string]any{})
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var export struct {
		ID string `json:"id"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &export); e != nil {
		t.Fatal(e)
	}
	s, _ := a.backupStorage.store(p.StoreID)
	c, _ := s.Snapshot()
	if e := s.Delete(context.Background(), p.ID, c.Revision); e != backupstore.ErrBusy {
		t.Fatal("ready export lost lease", e)
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+export.ID+"/download", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), ".tar") {
		t.Fatal("filename")
	}
	w = historyRequest(t, a, h, "GET", "/api/backup-export/"+export.ID, nil)
	if !strings.Contains(w.Body.String(), `"state":"transferred"`) {
		t.Fatal(w.Body)
	}
	if e := s.Delete(context.Background(), p.ID, c.Revision); e != nil {
		t.Fatal("completed export leaked lease", e)
	}
}
func TestBackupHistoryAuth(t *testing.T) {
	a, h, p := historyHTTPFixture(t)
	a.authManager.enabled = true
	for _, url := range []string{"/api/backup-history/CONTROL", "/api/backup-point/" + p.ID, "/api/backup-export/" + p.ID + "/download"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
		if w.Code != 401 {
			t.Fatal(url, w.Code)
		}
	}
}
