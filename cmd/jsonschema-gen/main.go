// Command jsonschema-gen generates Go types with inline validation from a JSON
// Schema document.
//
// Usage:
//
//	jsonschema-gen [flags] <schema.json>
//	jsonschema-gen -config gen.yaml
//
// Configuration may come from flags, a YAML config file (-config), or both;
// explicitly-set flags take precedence over the config file. The generated
// source is written to stdout unless -o (or the config's output) is given.
package main

import (
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/ChristopherDavenport/jsonschema/gen"
)

// fileConfig mirrors the CLI flags for the -config file. Pointer fields let us
// tell "absent" from "set to the zero value".
type fileConfig struct {
	Package      *string `yaml:"package"`
	RootName     *string `yaml:"rootName"`
	BaseURI      *string `yaml:"baseURI"`
	AssertFormat *bool   `yaml:"assertFormat"`
	Input          *string `yaml:"input"`
	Output         *string `yaml:"output"`
	EngineFallback *bool   `yaml:"engineFallback"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "jsonschema-gen:", err)
		os.Exit(1)
	}
}

func run() error {
	pkg := flag.String("package", "schema", "generated package name")
	root := flag.String("root", "Root", "Go type name for the root schema")
	out := flag.String("o", "", "output file (default: stdout)")
	assertFormat := flag.Bool("assert-format", false, "emit `format` assertions in Validate methods")
	baseURI := flag.String("base-uri", "", "base URI used to resolve references")
	engineFallback := flag.Bool("engine-fallback", false, "for types using if/then/else, dependentSchemas, not, or allOf, delegate Validate to the embedded schema + runtime engine (full conformance, adds a dependency on the jsonschema package)")
	configPath := flag.String("config", "", "YAML config file (flags override its values)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: jsonschema-gen [flags] <schema.json>")
		flag.PrintDefaults()
	}
	flag.Parse()

	input := flag.Arg(0)

	// Merge a config file underneath any explicitly-set flags.
	if *configPath != "" {
		fc, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		set := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if !set["package"] && fc.Package != nil {
			*pkg = *fc.Package
		}
		if !set["root"] && fc.RootName != nil {
			*root = *fc.RootName
		}
		if !set["base-uri"] && fc.BaseURI != nil {
			*baseURI = *fc.BaseURI
		}
		if !set["assert-format"] && fc.AssertFormat != nil {
			*assertFormat = *fc.AssertFormat
		}
		if !set["o"] && fc.Output != nil {
			*out = *fc.Output
		}
		if !set["engine-fallback"] && fc.EngineFallback != nil {
			*engineFallback = *fc.EngineFallback
		}
		if input == "" && fc.Input != nil {
			input = *fc.Input
		}
	}

	if input == "" {
		flag.Usage()
		return fmt.Errorf("no schema file given (positional argument or config `input`)")
	}

	data, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}

	src, err := gen.Generate(gen.Config{
		Package:        *pkg,
		RootName:       *root,
		BaseURI:        *baseURI,
		AssertFormat:   *assertFormat,
		EngineFallback: *engineFallback,
	}, data)
	if err != nil {
		return err
	}

	if *out == "" {
		_, err = os.Stdout.Write(src)
		return err
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
	return nil
}

func loadConfig(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &fc, nil
}
