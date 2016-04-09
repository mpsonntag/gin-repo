package git

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Repository struct {
	Path string
}

func InitBareRepository(path string) (*Repository, error) {

	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("Could not determine absolute path: %v", err)
	}

	cmd := exec.Command("git", "init", "--bare", path)
	err = cmd.Run()

	if err != nil {
		return nil, err
	}

	return &Repository{Path: path}, nil
}

func IsBareRepository(path string) bool {

	cmd := exec.Command("git", fmt.Sprintf("--git-dir=%s", path), "rev-parse", "--is-bare-repository")
	body, err := cmd.Output()

	if err != nil {
		return false
	}

	status := strings.Trim(string(body), "\n ")
	return status == "true"
}

func OpenRepository(path string) (*Repository, error) {

	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("git: could not determine absolute path")
	}

	if !IsBareRepository(path) {
		return nil, fmt.Errorf("git: not a bare repository")
	}

	return &Repository{Path: path}, nil
}

func (repo *Repository) ReadDescription() string {
	path := filepath.Join(repo.Path, "description")

	dat, err := ioutil.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(dat)
}

func (repo *Repository) WriteDescription(description string) error {
	path := filepath.Join(repo.Path, "description")

	// not atomic, fine for now
	return ioutil.WriteFile(path, []byte(description), 0666)
}

func (repo *Repository) HasAnnex() bool {
	d := filepath.Join(repo.Path, "annex")
	s, err := os.Stat(d)
	return err == nil && s.IsDir()
}

func (repo *Repository) InitAnnex() error {
	cmd := exec.Command("git", fmt.Sprintf("--git-dir=%s", repo.Path), "annex", "init", "gin")
	body, err := cmd.Output()

	if err != nil {
		return fmt.Errorf("git: init annex failed: %q", string(body))
	}

	return nil
}

func (repo *Repository) OpenObject(id SHA1) (Object, error) {
	idstr := id.String()
	opath := filepath.Join(repo.Path, "objects", idstr[:2], idstr[2:])

	obj, err := OpenObject(opath)

	if err == nil {
		return obj, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	indicies := repo.loadPackIndices()

	for _, f := range indicies {

		idx, err := PackIndexOpen(f)
		if err != nil {
			continue
		}

		pos, err := idx.FindSHA1(id)
		if err != nil {
			continue
		}

		off, err := idx.ReadOffset(pos)
		if err != nil {
			return nil, err
		}

		pf, err := OpenPackFile(f[:len(f)-4] + ".pack")
		if err != nil {
			return nil, err
		}

		return pf.ReadPackObject(off)
	}

	// from inspecting the os.isNotExist source it
	// seems that if we have "not found" in the message
	// os.IsNotExist() report true, which is what we want
	return nil, fmt.Errorf("git: object not found")
}

func (repo *Repository) loadPackIndices() []string {
	target := filepath.Join(repo.Path, "objects", "pack", "*.idx")
	files, err := filepath.Glob(target)

	if err != nil {
		panic(err)
	}

	return files
}
