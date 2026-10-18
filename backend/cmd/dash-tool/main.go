// dash-tool runs build-host workflows without a scripting runtime.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"dash.local/backend/internal/hosttools"
)

func run() error {
	root, err := hosttools.ProjectRoot()
	if err != nil {
		return err
	}
	tool := hosttools.New(root)
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: dash-tool setup|fixture|release|publish|build-media|fetch-release|sandbox-up|update-config")
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "tls":
		f := flag.NewFlagSet("tls", flag.ContinueOnError)
		destination := f.String("destination", "tls", "local certificate directory")
		hosts := f.String("hosts", "localhost,127.0.0.1,::1", "comma-separated device IP addresses and DNS names")
		if err := f.Parse(args); err != nil {
			return err
		}
		return tool.LocalTLS(*destination, *hosts)
	case "fixture":
		return tool.Fixture()
	case "sandbox-up":
		return tool.SandboxUp()
	case "setup":
		f := flag.NewFlagSet("setup", flag.ContinueOnError)
		check := f.Bool("check", false, "verify the build environment")
		if err := f.Parse(args); err != nil {
			return err
		}
		return tool.Setup(*check)
	case "release":
		f := flag.NewFlagSet("release", flag.ContinueOnError)
		source := f.Bool("source", false, "package a local source build")
		if err := f.Parse(args); err != nil {
			return err
		}
		return tool.Release(*source)
	case "publish":
		return tool.Publish()
	case "build-media":
		if len(args) == 0 {
			args = []string{"arm64", "amd64"}
		}
		for _, arch := range args {
			directory, err := tool.BuildMedia(arch)
			if err != nil {
				return err
			}
			fmt.Println("Media bundle:", directory)
		}
		return nil
	case "build-filesystems":
		if len(args) == 0 {
			args = []string{"arm64", "amd64"}
		}
		for _, arch := range args {
			directory, err := tool.BuildFilesystems(arch)
			if err != nil {
				return err
			}
			fmt.Println("USB filesystem bundle:", directory)
		}
		return nil
	case "fetch-release":
		f := flag.NewFlagSet("fetch-release", flag.ContinueOnError)
		var options hosttools.FetchOptions
		f.StringVar(&options.Channel, "channel", "main", "release channel")
		f.StringVar(&options.Release, "release", "", "release tag")
		f.StringVar(&options.Repository, "repo", "LBYPatrick/dash", "GitHub repository")
		f.StringVar(&options.Arch, "arch", "arm64", "device architecture")
		f.StringVar(&options.Destination, "destination", "", "new payload directory")
		f.StringVar(&options.LocalManifest, "local-manifest", "", "verify a local source build without network")
		if err = f.Parse(args); err != nil {
			return err
		}
		if options.Destination == "" {
			return fmt.Errorf("destination is required")
		}
		if options.LocalManifest != "" && !filepath.IsAbs(options.LocalManifest) {
			options.LocalManifest = filepath.Join(root, options.LocalManifest)
		}
		if !filepath.IsAbs(options.Destination) {
			options.Destination = filepath.Join(root, options.Destination)
		}
		identity, err := tool.FetchRelease(options)
		if err == nil {
			fmt.Println(identity.Tag)
		}
		return err
	case "update-config":
		if len(args) != 4 {
			return fmt.Errorf("update-config requires destination, repository, channel and source flag")
		}
		source, err := strconv.ParseBool(args[3])
		if err != nil {
			return err
		}
		data, err := json.Marshal(map[string]any{"repository": args[1], "channel": args[2], "automatic": !source})
		if err != nil {
			return err
		}
		return os.WriteFile(args[0], append(data, '\n'), 0600)
	default:
		return fmt.Errorf("unknown host command: %s", os.Args[1])
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dash-tool:", err)
		os.Exit(1)
	}
}
