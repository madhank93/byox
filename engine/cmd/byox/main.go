package main

import (
	"fmt"
	"os"

	"github.com/madhan/byox/course"
	"github.com/madhan/byox/internal/progress"
	"github.com/madhan/byox/internal/runner"
	"github.com/madhan/byox/internal/tui"
)

const usage = `byox — local CodeCrafters-style course runner

usage:
  byox                          launch TUI
  byox setup                    clone courses/testers, build, seed starters
  byox test <course>            run official tests for the current stage
  byox status                   show progress
  byox reset <course> --stage <slug>   rewind progress pointer
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := course.FindRoot()
	if err != nil {
		return err
	}
	reg, err := course.LoadRegistry(root)
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return tui.Start(root, reg)
	}
	switch args[0] {
	case "setup":
		return runner.Setup(root, reg, os.Stdout)
	case "test":
		if len(args) != 2 {
			return fmt.Errorf("usage: byox test <course>")
		}
		return cmdTest(root, reg, args[1])
	case "status":
		return cmdStatus(root, reg)
	case "reset":
		if len(args) != 4 || args[2] != "--stage" {
			return fmt.Errorf("usage: byox reset <course> --stage <slug>")
		}
		return cmdReset(root, reg, args[1], args[3])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		fmt.Print(usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func cmdTest(root string, reg *course.Registry, slug string) error {
	c, err := reg.Find(slug)
	if err != nil {
		return err
	}
	def, err := course.LoadDefinition(c.VendorDir(root))
	if err != nil {
		return err
	}
	prog, err := progress.Load(root)
	if err != nil {
		return err
	}
	idx := prog.CurrentIndex(slug, stageSlugs(def))
	if idx >= len(def.Stages) {
		fmt.Printf("course %s complete — all %d stages passed 🎉\n", slug, len(def.Stages))
		return nil
	}
	cur := def.Stages[idx]
	fmt.Printf("testing %s stage %d/%d: %s (%s)\n\n", slug, idx+1, len(def.Stages), cur.Name, cur.Slug)
	runErr := runner.Run(c, root, def.Stages, idx, os.Stdout)
	prog.RecordRun(slug, runErr == nil)
	if runErr != nil {
		prog.Save()
		return fmt.Errorf("stage %q failed", cur.Slug)
	}
	prog.MarkCompleted(slug, cur.Slug)
	if err := prog.Save(); err != nil {
		return err
	}
	if idx+1 < len(def.Stages) {
		fmt.Printf("\nstage %q passed ✓ — next up: %s (%s)\n", cur.Slug, def.Stages[idx+1].Name, def.Stages[idx+1].Slug)
	} else {
		fmt.Printf("\nstage %q passed ✓ — course complete 🎉\n", cur.Slug)
	}
	return nil
}

func cmdStatus(root string, reg *course.Registry) error {
	prog, err := progress.Load(root)
	if err != nil {
		return err
	}
	for _, c := range reg.Entries {
		def, err := course.LoadDefinition(c.VendorDir(root))
		if err != nil {
			fmt.Printf("%-14s not set up (run `just setup`)\n", c.Slug)
			continue
		}
		idx := prog.CurrentIndex(c.Slug, stageSlugs(def))
		if idx >= len(def.Stages) {
			fmt.Printf("%-14s %d/%d complete 🎉\n", c.Slug, len(def.Stages), len(def.Stages))
			continue
		}
		fmt.Printf("%-14s %d/%d — current: %s (%s)\n",
			c.Slug, idx, len(def.Stages), def.Stages[idx].Name, def.Stages[idx].Slug)
	}
	return nil
}

func cmdReset(root string, reg *course.Registry, slug, stage string) error {
	c, err := reg.Find(slug)
	if err != nil {
		return err
	}
	def, err := course.LoadDefinition(c.VendorDir(root))
	if err != nil {
		return err
	}
	prog, err := progress.Load(root)
	if err != nil {
		return err
	}
	if err := prog.ResetTo(slug, stage, stageSlugs(def)); err != nil {
		return err
	}
	if err := prog.Save(); err != nil {
		return err
	}
	fmt.Printf("%s progress rewound — current stage is now %q (solution code untouched)\n", slug, stage)
	return nil
}

func stageSlugs(def *course.Definition) []string {
	slugs := make([]string, len(def.Stages))
	for i, s := range def.Stages {
		slugs[i] = s.Slug
	}
	return slugs
}
