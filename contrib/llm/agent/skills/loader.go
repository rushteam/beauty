package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Loader 从某个来源加载技能(本地目录、远程仓库等)。返回的每个 Skill 需已通过校验。
type Loader interface {
	Load() ([]Skill, error)
}

// LocalSkills 从本地目录加载技能:Dir 下每个包含 SKILL.md 的**直接子目录**算一个技能;
// 该子目录里的 scripts/ 与 references/ 的文件名会被登记(读取时再做路径校验)。
type LocalSkills struct {
	Dir string
}

// Load 实现 Loader。
func (l LocalSkills) Load() ([]Skill, error) {
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return nil, fmt.Errorf("skills: 读目录 %s: %w", l.Dir, err)
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(l.Dir, e.Name())
		mdPath := filepath.Join(dir, "SKILL.md")
		data, err := os.ReadFile(mdPath)
		if err != nil {
			continue // 没有 SKILL.md 的子目录跳过(不是技能)
		}
		sk, err := parseSkill(data, dir)
		if err != nil {
			return nil, fmt.Errorf("skills: %s: %w", mdPath, err)
		}
		out = append(out, sk)
	}
	return out, nil
}

// parseSkill 解析并校验一个 SKILL.md,登记同目录下 scripts/、references/ 的文件名。
func parseSkill(data []byte, dir string) (Skill, error) {
	fm, body, err := parseSkillMD(data)
	if err != nil {
		return Skill{}, err
	}
	if err := fm.validate(); err != nil {
		return Skill{}, err
	}
	return Skill{
		Name:         fm.Name,
		Description:  fm.Description,
		Instructions: body,
		Scripts:      listFiles(filepath.Join(dir, "scripts")),
		References:   listFiles(filepath.Join(dir, "references")),
		SourcePath:   dir,
		License:      fm.License,
		AllowedTools: fm.AllowedTools,
		Metadata:     fm.Metadata,
	}, nil
}

// listFiles 返回目录下的常规文件名(不含子目录);目录不存在返回 nil。结果按名排序,稳定输出。
func listFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
