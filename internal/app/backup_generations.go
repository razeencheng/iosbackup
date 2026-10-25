package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"iosbackup/internal/backupstore"
	"iosbackup/internal/persistence"
)

var errGenerationUncertain = errors.New("backup generation needs reconciliation")

type generationChange struct {
	OldCredentialRef string `json:"old_credential_ref,omitempty"`
	ID               string `json:"id"`
	Target           string `json:"target"`
	Action           string `json:"action"`
}
type backupGenerations struct {
	Recovered   bool                              `json:"-"`
	Schema      int                               `json:"schema"`
	UDID        string                            `json:"udid"`
	Current     string                            `json:"current"`
	Generations map[string]backupstore.Generation `json:"generations"`
	Unresolved  []generationChange                `json:"unresolved,omitempty"`
	Pending     *generationChange                 `json:"pending,omitempty"`
}
type generationStore struct {
	mu       sync.Mutex
	verified map[string]bool
	root     string
	secrets  secretStore
	now      func() time.Time
	write    func(string, []byte, os.FileMode) error
}

// load 不根据旧密码或目录存在推断可复用基线，也不迁移备份数据。
func (s *generationStore) load(udid string) (backupGenerations, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(udid)
}
func (s *generationStore) loadLocked(udid string) (backupGenerations, error) {
	if !udidPattern.MatchString(udid) {
		return backupGenerations{}, backupstore.ErrInvalid
	}
	data, err := persistence.ReadSecretFile(filepath.Join(s.root, udid+".json"), 8<<20, true)
	if errors.Is(err, os.ErrNotExist) {
		return backupGenerations{Schema: 1, UDID: udid, Generations: map[string]backupstore.Generation{}}, nil
	}
	if err != nil {
		return backupGenerations{}, err
	}
	var state backupGenerations
	if err = decodeStrictJSON(data, &state); err != nil {
		return state, err
	}
	if state.Schema != 1 || state.UDID != udid || state.Generations == nil {
		return state, backupstore.ErrInvalid
	}
	for id, g := range state.Generations {
		if id != g.ID || g.Validate() != nil {
			return state, backupstore.ErrInvalid
		}
	}
	if state.Current != "" {
		if _, ok := state.Generations[state.Current]; !ok {
			return state, backupstore.ErrInvalid
		}
	}
	changes := append([]generationChange(nil), state.Unresolved...)
	if state.Pending != nil {
		changes = append(changes, *state.Pending)
	}
	for _, p := range changes {
		if !backupstore.ValidID(p.ID) || state.Generations[p.Target].ID == "" || (p.Action != "enable" && p.Action != "disable" && p.Action != "change") || (p.OldCredentialRef != "" && !backupstore.ValidID(p.OldCredentialRef)) {
			return state, backupstore.ErrInvalid
		}
	}
	state.Recovered = s.verified[udid]
	return state, nil
}
func (s *generationStore) save(state backupGenerations) error {
	if err := persistence.EnsurePrivateDirectory(s.root); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return backupstore.ErrCapacity
	}
	write := s.write
	if write == nil {
		write = persistence.WriteFileAtomic
	}
	err = write(filepath.Join(s.root, state.UDID+".json"), data, 0600)
	if s.verified == nil {
		s.verified = map[string]bool{}
	}
	s.verified[state.UDID] = err == nil
	return err
}
func credentialKey(udid, ref string) string { return "backup-history/" + udid + "/" + ref }
func (s *generationStore) password(udid, ref string) (string, error) {
	if !udidPattern.MatchString(udid) || !backupstore.ValidID(ref) {
		return "", backupstore.ErrInvalid
	}
	if s.secrets == nil || !s.secrets.Available() {
		return "", errEncryptionUnavailable
	}
	return s.secrets.GetSecret(credentialKey(udid, ref))
}
func (s *generationStore) generation(udid, kind, pw string) (backupstore.Generation, error) {
	id, err := backupstore.NewID()
	if err != nil {
		return backupstore.Generation{}, err
	}
	g := backupstore.Generation{ID: id, Kind: kind, CreatedAt: s.now().UTC()}
	if pw != "" {
		g.CredentialRef, err = backupstore.NewID()
		if err != nil {
			return g, err
		}
		// 每个引用只写一次；失败留下未引用密文也不删除可能已落盘的旧凭据。
		if err = s.secrets.SetSecret(credentialKey(udid, g.CredentialRef), pw); err != nil {
			return g, err
		}
	}
	return g, nil
}
func (s *generationStore) begin(udid, action, oldPW, newPW string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secrets == nil || !s.secrets.Available() {
		return "", errEncryptionUnavailable
	}
	if action != "enable" && action != "disable" && action != "change" {
		return "", backupstore.ErrInvalid
	}
	if (action != "disable" && newPW == "") || (action != "enable" && oldPW == "") {
		return "", backupstore.ErrInvalid
	}
	state, err := s.loadLocked(udid)
	if err != nil {
		return "", err
	}
	if state.Pending != nil {
		state.Unresolved = append(state.Unresolved, *state.Pending)
		state.Pending = nil
	}
	if state.Current == "" {
		pw, err := s.secrets.GetBackupPassword(udid)
		if err != nil && !errors.Is(err, errPasswordNotFound) {
			return "", err
		}
		if pw == "" {
			pw = oldPW
		}
		kind := "unknown"
		if pw != "" {
			kind = "encrypted"
		}
		g, err := s.generation(udid, kind, pw)
		if err != nil {
			return "", err
		}
		state.Current = g.ID
		state.Generations[g.ID] = g
	}
	kind := "encrypted"
	if action == "disable" {
		kind = "unencrypted"
		newPW = ""
	}
	target, err := s.generation(udid, kind, newPW)
	if err != nil {
		return "", err
	}
	state.Generations[target.ID] = target
	id, err := backupstore.NewID()
	if err != nil {
		return "", err
	}
	oldRef := ""
	if oldPW != "" {
		candidate, e := s.generation(udid, "encrypted", oldPW)
		if e != nil {
			return "", e
		}
		state.Generations[candidate.ID] = candidate
		oldRef = candidate.CredentialRef
	}
	state.Pending = &generationChange{ID: id, Target: target.ID, Action: action, OldCredentialRef: oldRef}
	// 必须先可靠保留旧凭据及意图，再允许手机侧发生不可回滚的变化。
	if err = s.save(state); err != nil {
		return "", err
	}
	return id, nil
}
func (s *generationStore) finish(udid, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(udid)
	if err != nil {
		return err
	}
	if state.Pending == nil || state.Pending.ID != id {
		return errGenerationUncertain
	}
	target := state.Generations[state.Pending.Target]
	if target.Kind == "encrypted" {
		pw, err := s.password(udid, target.CredentialRef)
		if err != nil {
			return err
		}
		if err = s.secrets.SetBackupPassword(udid, pw); err != nil {
			return err
		}
	} else if err = s.secrets.DeleteBackupPassword(udid); err != nil {
		return err
	}
	state.Current = target.ID
	state.Pending = nil
	// 写入失败（包含 rename 后失败）仍按待核对处理；不伪造手机侧回滚。
	return s.save(state)
}

// compatibleBaseline 只允许明确的同代、同凭据正常点。未知元数据需新建全量。
func (s backupGenerations) compatibleBaseline(p backupstore.Point, metadataKnown bool) bool {
	g, ok := s.Generations[s.Current]
	return s.Recovered && metadataKnown && ok && s.Pending == nil && g.Kind != "unknown" && p.State == "normal" && p.Generation == g && (g.Kind != "encrypted" || g.CredentialRef != "")
}
func (app *application) generationRegistry() *generationStore {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.backupGenerations == nil {
		app.backupGenerations = &generationStore{root: filepath.Join(app.paths.ConfigsRoot, "backup-generations", "v1"), secrets: app.secretStore, now: app.nowTime}
	}
	return app.backupGenerations
}

// recover 只确认可见元数据的持久性，不猜测未完成手机操作的结果。
func (s *generationStore) recover(udid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(udid)
	if err != nil {
		return err
	}
	if state.Current == "" {
		return nil
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, path := range []string{udid + ".json", "."} {
		f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		err = f.Sync()
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if s.verified == nil {
		s.verified = map[string]bool{}
	}
	s.verified[udid] = true
	return nil
}

func (s *generationStore) recoverExisting() error {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		udid := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Name() != udid+".json" || !udidPattern.MatchString(udid) {
			return backupstore.ErrInvalid
		}
		if err = s.recover(udid); err != nil {
			return err
		}
	}
	return nil
}
