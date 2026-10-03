package gen_test

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/arandu-io/aru/internal/gen"
	"github.com/arandu-io/aru/tests"
)

// consumerCeiling is the most the generated model layer may add to a package
// that uses it, once compiled. A package that sees a generic type of the model
// layer recompiles that layer inside itself, measured at megabytes per
// package; one that sees only the generated concrete types holds its own code
// and its calls, which is tens of kilobytes. The ceiling sits between the two
// with room for the code a real service adds, so crossing it means the model
// layer is being compiled here again rather than that the service grew.
//
// It is measured over a floor: a package that names one entity and calls
// nothing. What that package weighs is the hesape type closure every package
// touching the model core pays -- the export data of the core's types, and the
// stdlib iterator shapes their method signatures and fields instantiate -- and
// it is the core's to shrink, not the generator's. The test logs it.
const consumerCeiling = 150 << 10

// consumerAbsoluteCeiling is the most a consumer package may weigh in all,
// floor included: the target the model layer was moved to concrete types for.
const consumerAbsoluteCeiling = 1 << 20

// sizedModule is the module path of the project the size guard builds.
const sizedModule = "example.test/sized"

// TestAConsumerOfTheModelsCompilesNoneOfTheModelLayer is the guard on what
// generating a concrete type per entity is for.
//
// Three models, as make:model writes them with their factories, and two
// packages that use them the way an application does -- grouped wheres, eager
// loads, pages, chunks, cursors, collections, factories. Built with a cache of
// its own, so every byte measured is what this build wrote:
//
//   - each consumer package weighs no more than consumerCeiling;
//   - no symbol of the models or of a consumer is a shape instantiation of the
//     model layer, the pagination, the factories or the collections, nor any
//     shape instantiated over a type of the application;
//   - and a query run without a Grant does not compile, because the generated
//     surface is a forward of the core, not a way around it.
//
// The sizes are logged; run with -v to read them. The shape symbols that do
// appear are logged too, by count.
func TestAConsumerOfTheModelsCompilesNoneOfTheModelLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a project with a cache of its own: skipped under -short")
	}
	tool := goTool(t)
	listed, listErr := exec.Command(tool, tests.HesapeQuery...).Output()
	version, dir := tests.ModelCore(t, listed, listErr)

	root := t.TempDir()
	gomod := "module " + sizedModule + "\n\ngo 1.26\n\nrequire github.com/arandu-io/hesape " + version + "\n"
	if dir != "" {
		gomod += "\nreplace github.com/arandu-io/hesape => " + dir + "\n"
	}
	writeInto(t, filepath.Join(root, "go.mod"), []byte(gomod))

	for _, m := range []gen.Module{
		sized("user", gen.Field{Name: "name", Type: gen.TypeString, Required: true}, gen.Field{Name: "email", Type: gen.TypeEmail, Required: true}),
		sized("post", gen.Field{Name: "user_id", Type: gen.TypeUUID}, gen.Field{Name: "title", Type: gen.TypeString}, gen.Field{Name: "published", Type: gen.TypeBool}),
		sized("comment", gen.Field{Name: "post_id", Type: gen.TypeUUID}, gen.Field{Name: "body", Type: gen.TypeText}, gen.Field{Name: "posted_at", Type: gen.TypeTimestamp}),
	} {
		files, err := gen.GenerateModel(m, gen.ModelParts{Factory: true})
		if err != nil {
			t.Fatalf("GenerateModel(%s): %v", m.Name, err)
		}
		for _, f := range files {
			writeInto(t, filepath.Join(root, f.Path), f.Content)
		}
	}
	writeInto(t, filepath.Join(root, "app", "Models", "relations.go"), []byte(sizedRelations))
	writeInto(t, filepath.Join(root, "app", "Services", "blog.go"), []byte(sizedServices))
	writeInto(t, filepath.Join(root, "app", "Reports", "activity.go"), []byte(sizedReports))
	writeInto(t, filepath.Join(root, "app", "Floor", "floor.go"), []byte(`package floor

import models "`+sizedModule+`/app/Models"

// ID names one entity and calls nothing: what this package weighs is what
// touching the model core costs any package, before a query is written.
func ID(u *models.User) string { return u.ID }
`))

	cache := t.TempDir()
	run := func(args ...string) (string, error) {
		cmd := exec.Command(tool, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GOCACHE="+cache, "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GOSUMDB=off")
		if args[0] != "mod" {
			cmd.Env = append(cmd.Env, "GOPROXY=off")
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("mod", "download", "all"); err != nil {
		say := t.Skipf
		if os.Getenv("CI") != "" {
			say = t.Fatalf
		}
		say("the sized project was not built, so nothing here was proved: its modules could not be resolved: %v\n%s", err, out)
	}
	if out, err := run("build", "-p=2", "-trimpath", "./..."); err != nil {
		t.Fatalf("go build refuses the sized project:\n%s", out)
	}
	out, err := run("list", "-p=2", "-trimpath", "-export", "-f", "{{.ImportPath}} {{.Export}}", "./...")
	if err != nil {
		t.Fatalf("go list -export: %v\n%s", err, out)
	}

	exports := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if pkg, export, ok := strings.Cut(line, " "); ok {
			exports[strings.TrimPrefix(pkg, sizedModule+"/")] = export
		}
	}
	size := func(pkg string) int64 {
		info, err := os.Stat(exports[pkg])
		if err != nil {
			t.Fatalf("%s has no compiled export (%q): %v", pkg, exports[pkg], err)
		}
		return info.Size()
	}
	floor := size("app/Floor")
	t.Logf("%-20s %8.1f KB  (the floor: one entity named, nothing called)", "app/Floor", float64(floor)/1024)
	for _, pkg := range []string{"app/Models", "database/factories", "app/Services", "app/Reports"} {
		export := exports[pkg]
		weight := size(pkg)
		consumer := pkg == "app/Services" || pkg == "app/Reports"
		if !consumer {
			t.Logf("%-20s %8.1f KB", pkg, float64(weight)/1024)
		} else {
			t.Logf("%-20s %8.1f KB  (%.1f KB over the floor)", pkg, float64(weight)/1024, float64(weight-floor)/1024)
			if weight-floor > consumerCeiling {
				t.Errorf("%s compiles to %d bytes, %d over the floor, and a consumer of concrete models adds under %d: "+
					"the model layer is being compiled into it", pkg, weight, weight-floor, consumerCeiling)
			}
			if weight > consumerAbsoluteCeiling {
				t.Errorf("%s compiles to %d bytes, over the %d no consumer package may weigh", pkg, weight, consumerAbsoluteCeiling)
			}
		}

		symbols, err := run("tool", "nm", export)
		if err != nil {
			t.Fatalf("go tool nm %s: %v\n%s", pkg, err, symbols)
		}
		shapes, offending := 0, []string{}
		scanner := bufio.NewScanner(strings.NewReader(symbols))
		scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
		for scanner.Scan() {
			symbol := scanner.Text()
			if !strings.Contains(symbol, "go.shape") {
				continue
			}
			shapes++
			if overModelLayer(symbol) {
				offending = append(offending, strings.TrimSpace(symbol))
			}
		}
		t.Logf("%-20s %8d shape symbols, %d over the model layer or the application", pkg, shapes, len(offending))
		if len(offending) > 0 {
			t.Errorf("%s holds %d shape instantiations of the model layer or over an application type, first: %s",
				pkg, len(offending), offending[0])
		}
	}

	// And the Grant: a terminal of the generated query takes one, so leaving it
	// out is a compile error rather than a query.
	writeInto(t, filepath.Join(root, "app", "NoGrant", "nogrant.go"), []byte(`package nogrant

import (
	"context"

	"github.com/arandu-io/hesape/database/model"

	models "`+sizedModule+`/app/Models"
)

func everything(ctx context.Context, db model.DB) {
	_, _ = models.Users(db).Get(ctx)
}
`))
	out, err = run("build", "-p=2", "-trimpath", "./app/NoGrant/")
	if err == nil {
		t.Fatal("models.Users(db).Get(ctx) compiled without a Grant")
	}
	if !strings.Contains(out, "not enough arguments in call to models.Users(db).Get") {
		t.Errorf("the build failed, and not because the Grant is missing:\n%s", out)
	}
}

// instantiatesModelLayer matches a symbol of a generic function or type of the
// model layer, the pagination, the factories or the collections, instantiated:
// the package path, a name, then a type argument list.
var instantiatesModelLayer = regexp.MustCompile(
	`hesape/(database/model|database/model/factories|database/model/relations|pagination|collections)\.[^\s\[]*\[`)

// overApplicationType matches an instantiation whose type arguments name a
// type of the application.
var overApplicationType = regexp.MustCompile(`\[[^\]]*` + regexp.QuoteMeta(sizedModule+"/"))

// overModelLayer reports a shape symbol that instantiates the model layer or
// is instantiated over a type of the application.
//
// A shape of a core interface instantiating a generic of the standard library
// -- iter.Seq2[model.Entity, error] in the core's own signatures -- is neither:
// it is one small wrapper per method of that interface, the same in every
// package, and is counted in the floor.
func overModelLayer(symbol string) bool {
	return instantiatesModelLayer.MatchString(symbol) || overApplicationType.MatchString(symbol)
}

func sized(name string, fields ...gen.Field) gen.Module {
	return gen.Module{Name: name, Fields: fields, Tenant: true, ModulePath: sizedModule, Date: "2026_07_31"}
}

// sizedRelations is the custom code an application writes beside its models:
// relations registered in init and a local scope.
const sizedRelations = `package models

import "github.com/arandu-io/hesape/database/model"

func init() {
	userTable.Relate("posts", func(u *model.Model) model.Relation {
		return model.HasMany(u, postTable, "user_id", "id")
	})
	postTable.Relate("comments", func(p *model.Model) model.Relation {
		return model.HasMany(p, commentTable, "post_id", "id")
	})
	postTable.Relate("user", func(p *model.Model) model.Relation {
		return model.BelongsTo(p, userTable, "user_id", "id", "user")
	})
}

// Published is a local scope.
func (q *PostQuery) Published() *PostQuery {
	q.b.Where("published", true)
	return q
}
`

const sizedServices = `package services

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/database/query"
	"github.com/arandu-io/hesape/pagination"

	factories "` + sizedModule + `/database/factories"
	models "` + sizedModule + `/app/Models"
)

// Blog is a service over all three models.
type Blog struct{ db model.DB }

func (b *Blog) Authors(ctx context.Context, g auth.Grant, page int) (models.UserCollection, *pagination.LengthAwarePage, error) {
	return models.Users(b.db).
		Where(func(q *models.UserQuery) { q.Where("name", "!=", "").OrWhere("email", "!=", "") }).
		WhereHas("posts", func(sub *query.Builder) { sub.Where("published", "=", true) }).
		WithCount("posts").
		With("posts").
		Latest().
		Paginate(ctx, g, 15, page, pagination.Options{})
}

func (b *Blog) Publish(ctx context.Context, g auth.Grant, id string) (*models.Post, error) {
	post, err := models.Posts(b.db).FindOrFail(ctx, g, id)
	if err != nil {
		return nil, err
	}
	post.Published = true
	if _, err := post.Save(ctx, g); err != nil {
		return nil, err
	}
	return post.Fresh(ctx, g, "user")
}

func (b *Blog) Feed(ctx context.Context, g auth.Grant) (models.PostCollection, *pagination.Page, error) {
	return models.Posts(b.db).Published().With("comments").OrderByDesc("created_at").
		SimplePaginate(ctx, g, 20, 1, pagination.Options{})
}

func (b *Blog) Write(ctx context.Context, g auth.Grant, title string) (*models.Post, error) {
	post, err := models.Posts(b.db).New()
	if err != nil {
		return nil, err
	}
	post.Title = title
	_, err = post.Save(ctx, g)
	return post, err
}

func (b *Blog) Seed(ctx context.Context, g auth.Grant) (models.UserCollection, error) {
	return factories.Users(b.db).Count(3).State(func(u *models.User) { u.Name = "Ada" }).Create(ctx, g)
}
`

const sizedReports = `package reports

import (
	"context"

	"github.com/arandu-io/hesape/auth"
	"github.com/arandu-io/hesape/database/model"

	models "` + sizedModule + `/app/Models"
)

// Activity reads across the models the way a report does.
type Activity struct{ db model.DB }

func (a *Activity) Walk(ctx context.Context, g auth.Grant) (int, error) {
	seen := 0
	for comment, err := range models.Comments(a.db).Latest().Cursor(ctx, g) {
		if err != nil {
			return seen, err
		}
		if comment.Body != "" {
			seen++
		}
	}
	err := models.Users(a.db).Chunk(ctx, g, 100, func(users models.UserCollection, _ int) (bool, error) {
		if err := users.Load(ctx, g, "posts"); err != nil {
			return false, err
		}
		seen += len(users.ModelKeys())
		return true, nil
	})
	return seen, err
}

func (a *Activity) Totals(ctx context.Context, g auth.Grant) (int64, error) {
	users, err := models.Users(a.db).Count(ctx, g)
	if err != nil {
		return 0, err
	}
	posts, err := models.Posts(a.db).WhereNotNull("user_id").Count(ctx, g)
	return users + posts, err
}

func (a *Activity) Mine(ctx context.Context, g auth.Grant, ids []any) ([]any, error) {
	found, err := models.Posts(a.db).WhereIn("id", ids).Get(ctx, g)
	if err != nil {
		return nil, err
	}
	return found.Unique().Pluck("title"), nil
}
`
