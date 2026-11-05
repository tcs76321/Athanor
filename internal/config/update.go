package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// UpdatePersonas rewrites the persona assignments in an existing config file.
// It preserves comments and unrelated keys by editing a yaml.Node tree, writes
// a backup next to the file, and validates the new document with Load before
// the atomic rename. The running process keeps its old registry until restart;
// callers must tell the operator.
func UpdatePersonas(path string, updates map[string]PersonaConfig) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	if len(updates) == 0 {
		return nil
	}
	for role := range updates {
		if role != "wide" && role != "tall" && role != "main" && role != "security" && role != "alternative" {
			return fmt.Errorf("unknown persona role %q", role)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config root is not a mapping")
	}
	root := doc.Content[0]
	personas := mappingValue(root, "personas")
	if personas == nil {
		personas = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "personas"},
			personas)
	}
	for role, pc := range updates {
		roleNode := mappingValue(personas, role)
		if roleNode == nil {
			roleNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			personas.Content = append(personas.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: role},
				roleNode)
		}
		setScalar(roleNode, "model", pc.Model)
		if pc.Family != "" {
			setScalar(roleNode, "family", pc.Family)
		}
		if pc.ContextTarget > 0 {
			setScalarTag(roleNode, "context_target", strconv.Itoa(pc.ContextTarget), "!!int")
		}
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return writeConfigFile(path, out, data)
}

// JobPodResources is the operator-editable slice of the Job Pod limits.
type JobPodResources struct {
	MemoryMB  int
	CPUs      float64
	TmpfsMB   int
	PidsLimit int
}

// UpdateJobPodResources rewrites job_pod.memory_mb/cpus/tmpfs_mb/pids_limit,
// preserving the rest of the config and validating the result before the
// atomic rename.
func UpdateJobPodResources(path string, r JobPodResources) error {
	if path == "" {
		return fmt.Errorf("config path is empty")
	}
	if r.MemoryMB < 256 || r.MemoryMB > 65536 {
		return fmt.Errorf("job pod memory must be 256–65536 MB, got %d", r.MemoryMB)
	}
	if r.CPUs <= 0 || r.CPUs > 64 {
		return fmt.Errorf("job pod CPUs must be >0 and ≤64, got %v", r.CPUs)
	}
	if r.TmpfsMB < 64 || r.TmpfsMB > 32768 {
		return fmt.Errorf("job pod tmpfs must be 64–32768 MB, got %d", r.TmpfsMB)
	}
	if r.PidsLimit < 32 || r.PidsLimit > 8192 {
		return fmt.Errorf("job pod pids limit must be 32–8192, got %d", r.PidsLimit)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config root is not a mapping")
	}
	root := doc.Content[0]
	jobPod := mappingValue(root, "job_pod")
	if jobPod == nil {
		jobPod = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "job_pod"},
			jobPod)
	}
	setScalarTag(jobPod, "memory_mb", strconv.Itoa(r.MemoryMB), "!!int")
	setScalarTag(jobPod, "cpus", strconv.FormatFloat(r.CPUs, 'f', -1, 64), "!!float")
	setScalarTag(jobPod, "tmpfs_mb", strconv.Itoa(r.TmpfsMB), "!!int")
	setScalarTag(jobPod, "pids_limit", strconv.Itoa(r.PidsLimit), "!!int")

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	return writeConfigFile(path, out, data)
}

func writeConfigFile(path string, out, original []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".athanor-config-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting temporary config mode: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary config: %w", err)
	}
	// Validate the exact bytes that will replace the file.
	if _, err := Load(tmpName); err != nil {
		return fmt.Errorf("validating updated config: %w", err)
	}
	backup := path + ".bak"
	if err := os.WriteFile(backup, original, mode); err != nil {
		return fmt.Errorf("writing config backup: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// The Core Pod mounts a single config file into the container. A
		// rename over that mount point returns EBUSY/EXDEV, so fall back to
		// writing through the mount. The backup above is the recovery path.
		if werr := os.WriteFile(path, out, mode); werr != nil {
			return fmt.Errorf("replacing config (mounted file): %w", werr)
		}
	}
	// Keep the backup timestamped for the audit trail.
	stamped := fmt.Sprintf("%s.%s", backup, time.Now().UTC().Format("20060102T150405Z"))
	_ = os.Rename(backup, stamped)
	return nil
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setScalar(m *yaml.Node, key, value string) {
	setScalarTag(m, key, value, "!!str")
}

func setScalarTag(m *yaml.Node, key, value, tag string) {
	if m.Kind != yaml.MappingNode {
		m.Kind = yaml.MappingNode
		m.Tag = "!!map"
		m.Content = nil
	}
	if existing := mappingValue(m, key); existing != nil {
		existing.Kind = yaml.ScalarNode
		existing.Tag = tag
		existing.Value = value
		existing.Content = nil
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
}
