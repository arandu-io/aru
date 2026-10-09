package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/internal/gomod"
)

// The usage lines of the three make:mcp-* commands, in the dispatch table and
// in their refusals, naming every flag each one accepts.
const (
	makeMCPToolUsage     = `aru make:mcp-tool <Name> --service=<Entity> [--force] [--dry-run]`
	makeMCPResourceUsage = `aru make:mcp-resource <Name> --service=<Entity> [--force] [--dry-run]`
	makeMCPPromptUsage   = `aru make:mcp-prompt <Name> [--force] [--dry-run]`
)

// makeMCP returns the command that writes one tool, resource or prompt in
// app/Mcp, on the mcp module.
//
// The three are one command body because they are one decision: a type the
// server holds, a test that drives it through a server, and the wiring printed
// rather than performed -- including the `go get` of the module itself, which
// is an instruction and never an edit of go.mod.
func makeMCP(kind gen.MCPKind, usage string) func(args []string, stdout, stderr io.Writer) error {
	command := "make:mcp-" + string(kind)
	return func(args []string, stdout, stderr io.Writer) error {
		fs := flag.NewFlagSet(command, flag.ContinueOnError)
		fs.SetOutput(stderr)
		var service *string
		if kind != gen.MCPPrompt {
			service = fs.String("service", "", "the entity whose service it calls: Invoice")
		}
		force := fs.Bool("force", false, "overwrite the file and its test, preserving the custom blocks")
		dryRun := fs.Bool("dry-run", false, "print what would be written, and write nothing")

		name, args := takeName(args)
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
		if name == "" {
			return fmt.Errorf("usage: %s", usage)
		}
		if err := checkFlatTree(command, name); err != nil {
			return err
		}

		root, err := projectRoot()
		if err != nil {
			return err
		}
		modulePath, err := readModulePath(root)
		if err != nil {
			return err
		}

		spec := gen.MCPSpec{Type: gen.Exported(name), Kind: kind, ModulePath: modulePath}
		if service != nil {
			if *service == "" {
				return fmt.Errorf("%s: a %s calls a service, never a model: name it with --service=<Entity>\nusage: %s", command, kind, usage)
			}
			spec.Service = unsuffixed(*service, "Service")
			if err := requireServiceMethod(root, spec, kind); err != nil {
				return fmt.Errorf("%s: %w", command, err)
			}
		}

		files, err := gen.RenderMCP(spec)
		if err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
		if err := emit(command, root, files, *force, *dryRun, stdout); err != nil {
			return err
		}
		if *dryRun {
			return nil
		}
		fmt.Fprint(stdout, wiringMCP(root, spec))
		return nil
	}
}

// requireServiceMethod refuses a service that does not have the method the
// generated type calls: Get for a tool, List for a resource, in the shape
// make:module writes them.
func requireServiceMethod(root string, s gen.MCPSpec, kind gen.MCPKind) error {
	path := filepath.Join(root, "app", "Services", s.ServiceType()+".go")
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("app/Services/%s.go does not exist -- `aru make:module %s` writes it", s.ServiceType(), gen.Normalize(s.Service))
	}
	method, shape := "Get", `\) Get\(ctx context\.Context, actor auth\.Subject, id string\)`
	if kind == gen.MCPResource {
		method, shape = "List", `\) List\(ctx context\.Context, actor auth\.Subject, page int\)`
	}
	if !regexp.MustCompile(`func \(\w+ \*` + regexp.QuoteMeta(s.ServiceType()) + shape).Match(body) {
		return fmt.Errorf("app/Services/%s.go has no %s in the shape the generated %s calls: "+
			"(ctx context.Context, actor auth.Subject, %s), as `aru make:module` writes it",
			s.ServiceType(), method, kind, map[string]string{"Get": "id string", "List": "page int"}[method])
	}
	return nil
}

// wiringMCP is what to do with the file that was just written: take the module
// if the project does not have it, compose the server in bootstrap/app.go and
// mount it in routes/web.go, so the route table stays one table.
func wiringMCP(root string, s gen.MCPSpec) string {
	take := ""
	body, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	if _, ok := gomod.Parse(string(body)).Pinned(gen.MCPModule); !ok {
		take = fmt.Sprintf(`
This project does not require the mcp module yet. Take it first -- this
command does not edit go.mod:

    go get %s@%s
`, gen.MCPModule, gen.MCPRelease)
	}
	list := map[gen.MCPKind]string{gen.MCPTool: "Tools", gen.MCPResource: "Resources", gen.MCPPrompt: "Prompts"}[s.Kind]
	elem := map[gen.MCPKind]string{gen.MCPTool: "mcp.Tool", gen.MCPResource: "mcp.Resource", gen.MCPPrompt: "mcp.Prompt"}[s.Kind]
	svc := ""
	if s.Service != "" {
		svc = "services.New" + s.ServiceType() + "(db)"
	}
	return fmt.Sprintf(`%s
The file is written, the wiring is not. By hand, because the wiring is meant to
be readable:

  bootstrap/app.go -- the server, composed once, and handed to the routes

      mcpServer := &mcp.Server{
          Name:         "...",
          Version:      "1.0.0",
          Instructions: "What this application is for, read by the model first.",
          %s: []%s{%s},
      }

      MCP: mcpServer,        // in the routes.Deps literal

  routes/web.go -- the field on Deps, and the route in the custom block, behind
  the guard that puts the subject on the request

      MCP *mcp.Server

      r.Action("POST", "/mcp", mcp.Web(d.MCP), middleware.RequireAuth(d.Sessions)).Name("mcp")

RequireToken in place of RequireAuth serves a client holding an API token. The
imports are "%s" in both files and appmcp "%s" in bootstrap/app.go.
`, take, list, elem, s.Constructor(svc), gen.MCPModule, s.MCPImport())
}
