package backupstore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) Recover(ctx context.Context) (RecoveryReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var report RecoveryReport
	if s.closed {
		return report, os.ErrClosed
	}
	if len(s.leases) > 0 {
		return report, ErrBusy
	}
	s.uncertain = true
	if err := s.checkIdentity(); err != nil {
		return report, err
	}
	if err := s.loadCatalog(); err != nil {
		return report, err
	}
	transactions, err := s.names("transactions")
	if err != nil {
		return report, err
	}
	knownWork := map[string]bool{}
	knownDescriptors := map[string]bool{}
	for _, name := range transactions {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		if strings.HasPrefix(name, ".atomic-") {
			report.Unclaimed = append(report.Unclaimed, "transactions/"+name)
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if name != id+".json" || !ValidID(id) {
			return report, ErrUncertain
		}
		tx, err := s.readTransaction(id)
		if err != nil {
			return report, err
		}
		switch tx.Phase {
		case "work", "sealing":
			if _, ok := s.catalog.Points[id]; ok {
				return report, ErrUncertain
			}
			if _, err = s.root.Lstat(filepath.Join("objects", id)); !errors.Is(err, os.ErrNotExist) {
				return report, ErrUncertain
			}
			knownWork[id] = true
			knownDescriptors[id+".json"] = true
			report.Uncommitted = append(report.Uncommitted, id)
		case "ready":
			path := filepath.Join("work", id)
			if _, err = s.root.Lstat(path); errors.Is(err, os.ErrNotExist) {
				path = filepath.Join("objects", id)
			} else if err != nil {
				return report, err
			}
			if err = s.syncObject(ctx, path); err != nil {
				return report, err
			}
			if err = s.publish(tx); err != nil {
				return report, err
			}
			report.Recovered = append(report.Recovered, id)
		case "metadata":
			if err = s.finishMetadata(tx); err != nil {
				return report, err
			}
			report.Recovered = append(report.Recovered, id)
		case "deleting":
			if err = s.finishRecycle(ctx, tx); err != nil {
				return report, err
			}
			report.Recovered = append(report.Recovered, id)
		}
	}
	for id, point := range s.catalog.Points {
		var desc Point
		if err = s.readJSON(filepath.Join("descriptors", id+".json"), &desc); err != nil {
			return report, err
		}
		if desc != point {
			return report, ErrUncertain
		}
		if err = s.checkObjectShape(filepath.Join("objects", id)); err != nil {
			return report, err
		}
		knownDescriptors[id+".json"] = true
	}
	blocked := false
	for _, dir := range []string{"objects", "work", "descriptors"} {
		names, err := s.names(dir)
		if err != nil {
			return report, err
		}
		for _, name := range names {
			known := false
			switch dir {
			case "objects":
				_, known = s.catalog.Points[name]
			case "work":
				known = knownWork[name]
			case "descriptors":
				known = knownDescriptors[name]
			}
			if !known {
				report.Unclaimed = append(report.Unclaimed, filepath.Join(dir, name))
				if !strings.HasPrefix(name, ".atomic-") {
					blocked = true
				}
			}
		}
	}
	if blocked {
		return report, ErrUncertain
	}
	// 即使上次 rename 返回了同步错误，也要重新确认目录和引用持久性。
	if err = s.syncDirs("catalog.json", "store.json", ".", "work", "objects", "descriptors", "transactions"); err != nil {
		return report, err
	}
	s.uncertain = false
	return report, nil
}
func (s *Store) names(dir string) ([]string, error) {
	f, err := s.root.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for {
		batch, err := f.Readdirnames(256)
		names = append(names, batch...)
		if len(names) > 100000 {
			return nil, ErrCapacity
		}
		if err == io.EOF {
			sort.Strings(names)
			return names, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
