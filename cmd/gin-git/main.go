package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"os/exec"

	"github.com/G-Node/gin-repo/git"
	"github.com/docopt/docopt-go"
)

func main() {
	usage := `gin git tool.

Usage:
  gin-git show-pack <pack>
  gin-git show-delta <pack> <sha1>
  gin-git cat-file <sha1>
  gin-git rev-parse <ref>
 
  gin-git -h | --help
  gin-git --version

Options:
  -h --help     Show this screen.
  --version     Show version.
`
	args, _ := docopt.Parse(usage, nil, true, "gin-git 0.1", false)
	//fmt.Fprintf(os.Stderr, "%#v\n", args)

	repo, err := discoverRepository()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	if val, ok := args["rev-parse"].(bool); ok && val {
		revParse(repo, args["<ref>"].(string))
	} else if val, ok := args["show-pack"].(bool); ok && val {
		showPack(repo, args["<pack>"].(string))
	} else if val, ok := args["show-delta"].(bool); ok && val {
		showDelta(repo, args["<pack>"].(string), args["<sha1>"].(string))
	} else if oid, ok := args["<sha1>"].(string); ok {

		catFile(repo, oid)
	}
}

func discoverRepository() (*git.Repository, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	data, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	path := strings.Trim(string(data), "\n ")
	return &git.Repository{Path: path}, nil
}

func revParse(repo *git.Repository, refstr string) {
	ref, err := repo.OpenRef(refstr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(3)
	}

	id, err := ref.Resolve()
	var idstr string
	if err != nil {
		idstr = fmt.Sprintf("ERROR: %v", err)
	} else {
		idstr = fmt.Sprintf("%s", id)
	}

	fmt.Printf("%s\n", refstr)
	fmt.Printf(" └┬─ name: %s\n", ref.Name())
	fmt.Printf("  ├─ full: %s\n", ref.Fullname())
	fmt.Printf("  └─ SHA1: %s\n", idstr)
}

func catFile(repo *git.Repository, idstr string) {
	id, err := git.ParseSHA1(idstr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid object id: %v", err)
		os.Exit(3)
	}

	obj, err := repo.OpenObject(id)

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	printObject(obj, "")
	obj.Close()
}

func showDelta(repo *git.Repository, packid string, idstr string) {
	oid, err := git.ParseSHA1(idstr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid object id: %v", err)
		os.Exit(3)
	}

	if !strings.HasPrefix(packid, "pack-") {
		packid = "pack-" + packid
	}

	path := filepath.Join(repo.Path, "objects", "pack", packid)
	idx, err := git.PackIndexOpen(path + ".idx")

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	obj, err := idx.OpenObject(oid)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	delta, ok := obj.(*git.Delta)
	if !ok {
		fmt.Fprintf(os.Stderr, "Object with %s is not Delta", oid)
		os.Exit(1)
	}

	pf := ""
	fmt.Printf("%s Delta [%d]\n", pf, delta.Size())
	fmt.Printf("%s   └─ data: %v\n", pf, delta.Offset)

	if obj.Type() == git.ObjOFSDelta {
		fmt.Printf("%s   ├─ off: %v\n", pf, delta.BaseOff)
	} else {
		fmt.Printf("%s   ├─ ref: %v\n", pf, delta.BaseRef)
	}

	decoder := git.NewDeltaDecoder(delta)
	if !decoder.Setup() {
		fmt.Fprintln(os.Stderr, decoder.Err())
		os.Exit(3)
	}

	fmt.Printf("%s   └┬─ Instructions\n", pf)
	for decoder.NextOp() {
		op := decoder.Op()
		switch op.Op {
		case git.DeltaOpCopy:
			fmt.Printf("%s     ├─ Copy: %d @ %d\n", pf, op.Size, op.Offset)
		case git.DeltaOpInsert:
			fmt.Printf("%s     ├─ Insert %d\n", pf, op.Size)
		}
	}

}

func printObject(obj git.Object, prefix string) {

	switch obj := obj.(type) {
	case *git.Commit:
		fmt.Printf("Commit [%v]\n", obj.Size())
		fmt.Printf("%s └┬─ tree:      %s\n", prefix, obj.Tree)
		fmt.Printf("%s  ├─ parent:    %s\n", prefix, obj.Parent)
		fmt.Printf("%s  ├─ author:    %s\n", prefix, obj.Author)
		fmt.Printf("%s  ├─ committer: %s\n", prefix, obj.Committer)
		fmt.Printf("%s  └─ message:   [%.40s...]\n", prefix, obj.Message)
	case *git.Tree:
		fmt.Printf("Tree [%v]\n", obj.Size())

		for obj.Next() {
			entry := obj.Entry()
			fmt.Printf("%s ├─ %08o %-7s %s %s\n", prefix, entry.Mode, entry.Type, entry.ID, entry.Name)
		}

		if err := obj.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "%sERROR: %v", prefix, err)
		}
	case *git.Blob:
		fmt.Printf("Blob [%v]\n", obj.Size())
		_, err := io.Copy(os.Stdout, obj)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%sERROR: %v", prefix, err)
		}

	case *git.Tag:
		fmt.Printf("Tag [%v]\n", obj.Size())
		fmt.Printf("%s └┬─ object:    %s\n", prefix, obj.Object)
		fmt.Printf("%s  ├─ type:      %v\n", prefix, obj.ObjType)
		fmt.Printf("%s  ├─ tagger:    %s\n", prefix, obj.Tagger)
		fmt.Printf("%s  └─ message:   [%.40s...]\n", prefix, obj.Message)

	default:
		fmt.Printf("%s%v [%v]\n", prefix, obj.Type(), obj.Size())
	}

}

func showPack(repo *git.Repository, packid string) {
	if !strings.HasPrefix(packid, "pack-") {
		packid = "pack-" + packid
	}

	path := filepath.Join(repo.Path, "objects", "pack", packid)
	idx, err := git.PackIndexOpen(path + ".idx")

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	data, err := idx.OpenPackFile()

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for i := byte(0); i < 255; i++ {
		lead, prefix := "├─", "│"
		if i == 254 {
			lead, prefix = "└─", " "
		}
		fmt.Printf("%s[%02x]\n", lead, i)

		var oid git.SHA1

		s, e := idx.FO.Bounds(i)
		for k := s; k < e; k++ {
			lead := "├─"
			pf := prefix + " │"
			if e-k < 2 {
				lead = "└─┬"
				pf = prefix + "  "
			}

			fmt.Printf("%s %s", prefix, lead)
			err := idx.ReadSHA1(&oid, k)
			if err != nil {
				fmt.Printf(" ERROR: %v\n", err)
				continue
			}

			fmt.Printf("%s\n", oid)

			off, err := idx.ReadOffset(k)
			if err != nil {
				fmt.Printf(" ERROR: %v\n", err)
				continue
			}

			obj, err := data.AsObject(off)
			if err != nil {
				fmt.Printf(" ERROR: %v\n", err)
				continue
			}

			switch obj.Type() {
			case git.ObjCommit:
				fallthrough
			case git.ObjTree:
				fallthrough
			case git.ObjTag:
				fmt.Printf("%s └─", pf)
				printObject(obj, pf+"  ")
				continue
			}

			switch c := obj.(type) {

			case *git.Delta:
				fmt.Printf("%s └─Delta [%d, %d, %v]\n", pf, k, off, obj.Size())
				fmt.Printf("%s   └─ data: %v\n", pf, c.Offset)

				if obj.Type() == git.ObjOFSDelta {
					fmt.Printf("%s   └─ off: %v\n", pf, c.BaseOff)
				} else {
					fmt.Printf("%s   └─ ref: %v\n", pf, c.BaseRef)
				}
			default:
				fmt.Printf("%s └─ %s %d, %d, [%d]\n", pf, obj.Type(), k, off, obj.Size())

			}

			//NB: we don't close the obj here
			// because we would close the pack data
			// file with that too, we actually might
			// leak some zlib.Readers on the way too
		}

	}

}
