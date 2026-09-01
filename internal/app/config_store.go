package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const backupConfigSchemaVersion = 1

var errUnsupportedBackupConfigSchema = errors.New("不支持的备份配置 schema_version")

type backupConfigEnvelope struct {
	SchemaVersion int                     `json:"schema_version"`
	Configs       map[string]backupConfig `json:"configs"`
}

type backupConfigStore struct {
	mu           sync.Mutex
	path         string
	lastGoodPath string
	allowedRoots []string
	configs      map[string]backupConfig
}

func newBackupConfigStore(path string, allowedRoots []string) *backupConfigStore {
	return &backupConfigStore{
		path:         path,
		lastGoodPath: path + ".last-good",
		allowedRoots: append([]string(nil), allowedRoots...),
		configs:      make(map[string]backupConfig),
	}
}

func (s *backupConfigStore) Load() (map[string]backupConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.configs = make(map[string]backupConfig)
		return cloneBackupConfigs(s.configs), nil
	}
	if err != nil {
		return nil, err
	}

	configs, legacy, err := s.decode(data)
	if err != nil {
		if errors.Is(err, errUnsupportedBackupConfigSchema) {
			return nil, err
		}
		fallback, fallbackErr := os.ReadFile(s.lastGoodPath)
		if fallbackErr != nil {
			return nil, fmt.Errorf("读取备份配置失败: %v；last-good 不可用: %w", err, fallbackErr)
		}
		configs, _, fallbackErr = s.decode(fallback)
		if fallbackErr != nil {
			return nil, fmt.Errorf("读取备份配置失败: %v；last-good 损坏: %w", err, fallbackErr)
		}
	}

	if legacy {
		if err := s.saveLocked(configs); err != nil {
			return nil, fmt.Errorf("迁移旧备份配置: %w", err)
		}
	}
	s.configs = cloneBackupConfigs(configs)
	return cloneBackupConfigs(configs), nil
}

func (s *backupConfigStore) Put(cfg backupConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateBackupConfig(cfg, s.allowedRoots); err != nil {
		return err
	}
	next := cloneBackupConfigs(s.configs)
	next[cfg.UDID] = cfg
	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.configs = next
	return nil
}

func (s *backupConfigStore) Replace(configs map[string]backupConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]backupConfig, len(configs))
	for key, cfg := range configs {
		cfg.UDID = key
		if err := validateBackupConfig(cfg, s.allowedRoots); err != nil {
			return fmt.Errorf("配置 %s: %w", key, err)
		}
		next[key] = cfg
	}
	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.configs = next
	return nil
}

func (s *backupConfigStore) decode(data []byte) (map[string]backupConfig, bool, error) {
	var probe struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, false, err
	}

	if probe.SchemaVersion != nil {
		if *probe.SchemaVersion > backupConfigSchemaVersion || *probe.SchemaVersion < 1 {
			return nil, false, fmt.Errorf("%w: %d", errUnsupportedBackupConfigSchema, *probe.SchemaVersion)
		}
		var envelope backupConfigEnvelope
		if err := decodeStrictJSON(data, &envelope); err != nil {
			return nil, false, err
		}
		if envelope.Configs == nil {
			envelope.Configs = make(map[string]backupConfig)
		}
		if err := s.validateMap(envelope.Configs); err != nil {
			return nil, false, err
		}
		return envelope.Configs, false, nil
	}

	legacy := make(map[string]backupConfig)
	if err := decodeStrictJSON(data, &legacy); err != nil {
		return nil, false, err
	}
	if err := s.validateMap(legacy); err != nil {
		return nil, false, err
	}
	return legacy, true, nil
}

func (s *backupConfigStore) validateMap(configs map[string]backupConfig) error {
	for key, cfg := range configs {
		cfg.UDID = key
		configs[key] = cfg
		if err := validateBackupConfig(cfg, s.allowedRoots); err != nil {
			return fmt.Errorf("配置 %s: %w", key, err)
		}
	}
	return nil
}

func (s *backupConfigStore) saveLocked(configs map[string]backupConfig) error {
	envelope := backupConfigEnvelope{SchemaVersion: backupConfigSchemaVersion, Configs: configs}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.lastGoodPath, data, 0600); err != nil {
		return fmt.Errorf("写入 last-good: %w", err)
	}
	if err := writeFileAtomic(s.path, data, 0600); err != nil {
		return err
	}
	return nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("JSON 只能包含一个值")
		}
		return err
	}
	return nil
}

func cloneBackupConfigs(configs map[string]backupConfig) map[string]backupConfig {
	clone := make(map[string]backupConfig, len(configs))
	for key, cfg := range configs {
		clone[key] = cfg
	}
	return clone
}
