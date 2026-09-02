package doctor

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arandu-io/aru/internal/kyse"
	"github.com/arandu-io/aru/internal/manifest"
	"github.com/arandu-io/aru/internal/testlayout"
)

// rules is the whole check surface. Each one exists because something real can
// go wrong that the compiler cannot see -- and each message says what breaks,
// not which rule was violated.
//
// Adding a rule that rejects existing code is a breaking change: it enters as a
// warning in a minor and becomes an error in the next major.
//
// What belongs in this slice is a check about the application aru generates:
// the tree with app/, routes/ and resources/views/ in it. A check about aru's
// own source belongs in .golangci.yml at the root instead, and the two cannot
// be confused by accident -- doctor refuses a directory that is not an Arandu
// project, and the linter never sees one.
//
// The tree decides that, not the topic. Two rules below find SQL assembled by
// hand, which an off-the-shelf linter also finds, and they stay here: whoever
// runs `aru doctor` is not required to run anything else, so a report that
// verified authorization and left injection to a second tool would be half an
// answer.
//
// The last three run only on the performance profile, and they say so in their
// own first lines rather than being kept in a second slice: what they report is
// correct code on the conventional profile, which is a fact about each rule and
// not about how the set is assembled.
var rules = []func(*project) []Finding{
	unreadableFiles,
	repositoryNeedsPolicy,
	repositoryMethodNeedsGrant,
	policyMustBeOpened,
	controllerMustNotReachData,
	controllerMustNotReachTheRepository,
	tenantMustComeFromTheGrant,
	systemGrantIsAudited,
	noBuiltSQL,
	sensitiveFieldNeedsRedaction,
	sessionMustRotateOnLogin,
	viewDataMustBeAStruct,
	viewMustExist,
	declaredPermissionsMatchTheCode,
	viewsKeepNoStateInTheBrowser,
	tenantMustScopeTheSQL,
	theOutboxTableTravelsWithWhatWritesToIt,
	resourceNotReauthorized,
	rawOutputIsAComponent,
	noRetiredModuleIsImported,
	testsAreWhereTheyCanRun,
	migrationsMustReachTheBinary,
	addedColumnsMustBeNullable,
	migrationsMustBeReversible,
	theProfileIsDeclared,
	queriesReachOneAggregate,
	transactionsStayInsideOneAggregate,
}

// 0. A file doctor could not read makes every other rule unreliable.
//
// Skipping an unparsable file silently would leave the syntax error to the
// compiler, which reports it better -- and that is beside the point. Every rule
// here reasons over the whole file set, so a project whose InvoicePolicy.go does
// not parse looks exactly like a project with no policy for Invoice: an invented
// finding, pointing at the wrong file, telling the author to write a policy they
// had already written.
//
// Reporting it first and as an error is the honest answer: what follows is
// incomplete, and here is why.
func unreadableFiles(p *project) []Finding {
	var out []Finding
	for _, u := range p.unreadable {
		out = append(out, Finding{
			Rule: "file-does-not-parse", Severity: Error,
			File: u.rel, Line: u.line,
			Message: "this file does not parse: " + u.reason,
			Why:     "doctor reads the whole project to answer questions about it, so a file it cannot read makes the rest of this report incomplete -- and can make it wrong: an entity whose policy does not parse looks like an entity with no policy. Fix the syntax and run again.",
		})
	}
	return out
}

// entityPlace is where an entity was found, for a finding that names it.
type entityPlace struct {
	rel  string
	line int
}

// repositoriesAndPolicies collects, per entity, where its repository is and
// whether it has a policy.
//
// Both the path and the type name are read. The path is the convention --
// app/Repositories/InvoiceRepository.go -- and it keeps working on a file that
// does not parse. The type name is the truth: an InvoicePolicy declared inside a
// file called Policies.go is still the policy of Invoice, and a rule that only
// looked at file names would demand a second one.
func repositoriesAndPolicies(p *project) (map[string]entityPlace, map[string]bool) {
	repositories := map[string]entityPlace{}
	policies := map[string]bool{}

	record := func(entity string, at entityPlace) {
		if entity == "" {
			return
		}
		if _, seen := repositories[entity]; !seen {
			repositories[entity] = at
		}
	}

	for _, f := range p.files {
		if f.isTest {
			continue
		}
		switch f.category {
		case "Repositories":
			record(f.entity, entityPlace{f.rel, 1})
		case "Policies":
			if f.entity != "" {
				policies[f.entity] = true
			}
		}

		f.types(func(ts *ast.TypeSpec) {
			name := ts.Name.Name
			switch {
			case strings.HasSuffix(name, "Repository"):
				record(strings.TrimSuffix(name, "Repository"), entityPlace{f.rel, f.line(ts)})
			case strings.HasSuffix(name, "Policy"):
				if entity := strings.TrimSuffix(name, "Policy"); entity != "" {
					policies[entity] = true
				}
			}
		})
	}
	return repositories, policies
}

// 1. A repository without a policy for the same entity means the entity is
// reachable and nobody decided who may reach it.
//
// app/Policies/ is not a convention an organized team chooses to keep: it is
// skeleton, and this is the rule that makes it so.
func repositoryNeedsPolicy(p *project) []Finding {
	repositories, policies := repositoriesAndPolicies(p)

	// One finding per entity, and every entity -- not the first one and then
	// stop. Breaking out after the first would make a project with three
	// unprotected entities report one, and the author would find the next only
	// after fixing that one and running again.
	entities := make([]string, 0, len(repositories))
	for entity := range repositories {
		entities = append(entities, entity)
	}
	sort.Strings(entities)

	var out []Finding
	for _, entity := range entities {
		if policies[entity] {
			continue
		}
		// This rule concludes from an absence, so it has to know whether the
		// absence is real. See project.blind.
		if p.blind(entity) {
			continue
		}
		at := repositories[entity]
		out = append(out, Finding{
			Rule: "repository-without-policy", Severity: Error,
			File: at.rel, Line: at.line,
			Message: entity + " has a repository and no policy",
			Why:     "the entity is reachable and nobody decided who may reach it. Run `aru make:policy` or write app/Policies/" + entity + "Policy.go.",
		})
	}
	return out
}

// isRepository reports whether a method belongs to a repository.
//
// It answers the narrow question -- is this filed as a repository, or named
// like one -- and it is still the right question for repositoryNeedsPolicy,
// which is about the shape of the tree. The four authorization rules ask
// reachesApplicationData instead, because a model-based project has neither the
// directory nor the name and the four of them would go quiet together.
//
// The directory answers for a project that follows the tree, and the receiver
// name answers for the one that does not -- a type called InvoiceRepository is a
// repository wherever somebody filed it.
//
// The suffix, not the substring. Asking whether the receiver CONTAINS "Repo"
// makes ReportPolicy, Reporter and Reposition repositories: a read model that
// takes a Grant and hands it to the repository below is reported for not
// checking it, and the code is correct. A rule that fires on correct code is how
// a tool teaches people to ignore it.
func isRepository(f *file, fn *ast.FuncDecl) bool {
	if f.category == "Repositories" {
		return true
	}
	// Suffix, not substring, and both spellings.
	//
	// strings.Contains(t, "Repo") classified ReportPolicy, Reporter and
	// Reposition as repositories and produced a false positive on each. Narrowing
	// it to HasSuffix("Repository") fixed those three and lost every type ending
	// in Repo -- and this predicate gates all four authorization rules at once,
	// so a type it does not see is a type where none of them apply. Trading a
	// noisy rule for a blind one is the worse half of the trade.
	//
	// Both suffixes exclude the three names above, because none of them ends in
	// Repo or Repository.
	return looksLikeRepository(receiverType(fn))
}

// looksLikeRepository is the naming half of isRepository, split out so the two
// spellings and the three near-misses can be pinned by name in a test.
func looksLikeRepository(t string) bool {
	return strings.HasSuffix(t, "Repository") || strings.HasSuffix(t, "Repo")
}

// databaseCall reports whether a call goes through a database handle.
//
// It is the database/sql surface, by method name: doctor never type-checks, so
// what it can see is that something called QueryContext on something. A method
// of a repository that calls one of these has reached the table.
func databaseCall(name string) bool {
	switch {
	case strings.HasSuffix(name, ".Query"),
		strings.HasSuffix(name, ".QueryContext"),
		strings.HasSuffix(name, ".QueryRow"),
		strings.HasSuffix(name, ".QueryRowContext"),
		strings.HasSuffix(name, ".Exec"),
		strings.HasSuffix(name, ".ExecContext"),
		strings.HasSuffix(name, ".Prepare"),
		strings.HasSuffix(name, ".PrepareContext"),
		strings.HasSuffix(name, ".Begin"),
		strings.HasSuffix(name, ".BeginTx"):
		return true
	}
	return false
}

// reachesTheDatabase reports whether the body of a method touches the handle.
func reachesTheDatabase(fn *ast.FuncDecl) bool {
	return funcBodyContains(fn, databaseCall)
}

// grantChecks counts the calls to Check in a body, and how many of them have
// their answer used.
//
// Asking only whether a call exists is what let `_ = g.Check(x)` through: the
// method asks the question, throws the answer away, and reads like one that
// authorizes. That is worse than leaving the check out, because a reader stops
// looking.
//
// Used means the value goes somewhere: returned, tested in an if, or assigned to
// a name -- which is then almost always the err of the line below. Discarded
// means the two shapes that provably drop it: assignment to the blank identifier
// and a bare call statement.
func grantChecks(fn *ast.FuncDecl) (total, used int) {
	if fn.Body == nil {
		return 0, 0
	}

	isCheck := func(call *ast.CallExpr) bool {
		return strings.HasSuffix(callName(call), ".Check")
	}
	all := map[*ast.CallExpr]bool{}
	discarded := map[*ast.CallExpr]bool{}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if isCheck(x) {
				all[x] = true
			}
		case *ast.ExprStmt:
			if call, ok := x.X.(*ast.CallExpr); ok && isCheck(call) {
				discarded[call] = true
			}
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, rhs := range x.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || !isCheck(call) {
					continue
				}
				if id, ok := x.Lhs[i].(*ast.Ident); ok && id.Name == "_" {
					discarded[call] = true
				}
			}
		}
		return true
	})
	return len(all), len(all) - len(discarded)
}

// 2. Every exported repository method that touches the handle must take a Grant
// and check it.
//
// Three ways to lose the policy, one rule, because they are one mistake seen
// from three sides:
//
//   - the method never declares the Grant, so there is no signature to satisfy;
//   - it declares it and never checks it, so a Grant issued for another action
//     passes;
//   - it checks it into the blank identifier, so the answer is thrown away.
//
// The first one matters most, and it is the one a signature check misses:
// skipping every method that does not take a Grant assumes the signature is the
// enforcement. It applies to List and Find exactly as it applies to Create -- a
// read model, a report, a projection or an export with no policy is a tenant
// leak with a technical name.
//
// Only exported methods. An unexported helper that runs the query for the
// exported method above it is the shape people write, and the exported one is
// already audited: reporting the helper as well would be a second finding for
// one fact, and that is how a report becomes noise.
func repositoryMethodNeedsGrant(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if !reachesApplicationData(f, fn) || !ast.IsExported(fn.Name.Name) {
				continue
			}
			file, line := f.at(fn)

			if !takesGrant(fn) {
				if !reachesTheDatabase(fn) {
					continue
				}
				out = append(out, Finding{
					Rule: "grant-not-received", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " reaches the database and receives no Grant",
					Why:     "reading is authorized exactly like writing: a query with no policy is a tenant leak with a technical name. A read model, a report, a projection and an export are not exceptions. Take a security.Grant and start the method with: if err := g.Check(Action...); err != nil { return err }",
				})
				continue
			}

			total, used := grantChecks(fn)
			switch {
			case total == 0:
				out = append(out, Finding{
					Rule: "grant-not-checked", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " receives a Grant and never checks it",
					Why:     "a Grant issued for another action would pass. Start the method with: if err := g.Check(Action...); err != nil { return err }",
				})
			case used == 0:
				out = append(out, Finding{
					Rule: "grant-check-discarded", Severity: Error,
					File: file, Line: line,
					Message: fn.Name.Name + " calls Check and discards the answer",
					Why:     "`_ = g.Check(...)` asks the question and throws it away, so the method is unauthorized while reading like one that is not -- which is worse than leaving the check out, because the next reader stops looking. Return the error: if err := g.Check(Action...); err != nil { return err }",
				})
			}
		}
	}
	return out
}

func takesGrant(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		if sel, ok := p.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Grant" {
			return true
		}
	}
	return false
}

// 3. A generated policy denies everything. Left that way, the entity is
// unreachable -- and worse, it looks finished.
func policyMustBeOpened(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Can" {
				continue
			}
			receiver := receiverType(fn)
			if f.category != "Policies" && !strings.Contains(receiver, "Policy") {
				continue
			}
			if returnsNil(fn) {
				continue
			}

			entity := strings.TrimSuffix(receiver, "Policy")
			if entity == "" {
				entity = f.entity
			}
			if entity == "" {
				entity = "this entity"
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "policy-never-opened", Severity: Warning,
				File: file, Line: line,
				Message: "the policy of " + entity + " denies every action",
				Why:     "this is how `aru make:policy` generates it, on purpose. Open what the application needs inside the custom block -- until then every request that reaches this entity is refused.",
			})
		}
	}
	return out
}

func returnsNil(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		if id, ok := ret.Results[0].(*ast.Ident); ok && id.Name == "nil" {
			found = true
			return false
		}
		return true
	})
	return found
}

// requestPath names the kind of file when it sits on the path of a request, and
// reports false when it does not.
//
// The two rules below cannot ask for the Controllers category alone, because the
// request does not arrive only there. A handler written inline in the custom
// block of routes/web.go -- which the skeleton invites people to write in -- and
// a middleware in app/Http/Middleware are the same request, one layer earlier,
// with the same database under them.
func requestPath(f *file) (string, bool) {
	switch {
	case f.category == "Controllers":
		return "controller", true
	case f.category == "Middleware":
		return "middleware", true
	case strings.HasPrefix(f.rel, "routes/"):
		return "route file", true
	}
	return "", false
}

// 4. A handler that reaches the data package is a handler that skipped the
// service, and therefore the policy.
func controllerMustNotReachData(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		kind, onTheRequestPath := requestPath(f)
		if f.isTest || !onTheRequestPath {
			continue
		}
		for path := range f.imports {
			if !strings.HasSuffix(path, "/framework/data") {
				continue
			}
			// data.Query is the pagination type and belongs in a controller; the
			// rest of the package does not.
			onlyQuery := true
			ast.Inspect(f.ast, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "data" && sel.Sel.Name != "Query" {
					onlyQuery = false
					return false
				}
				return true
			})
			if onlyQuery {
				continue
			}
			out = append(out, Finding{
				Rule: "handler-reaches-data", Severity: Error,
				File: f.rel, Line: 1,
				Message: "this " + kind + " uses the data package beyond data.Query",
				Why:     "a handler that reaches the database skipped the service, and therefore the policy -- whether it is a controller, a middleware, or written inline in the route table. Move the call into app/Services, where the Grant is issued.",
			})
			break
		}

		// The same arrow, on the route the model opened. A controller that names
		// a model type in its view data is fine and common; one that queries
		// through the model package has skipped the service exactly as the one
		// above did, and the compiler cannot see either.
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !callsTheModelPackage(f, fn) {
				continue
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "handler-reaches-the-model", Severity: Error,
				File: file, Line: line,
				Message: fn.Name.Name + " queries through the model from a " + kind,
				Why:     "a handler that reads a row itself skipped the service, and therefore the policy that would have issued the Grant. Naming a model type is fine -- view data is full of them; opening a query is not. Move the call into app/Services.",
			})
			break
		}
	}
	return out
}

// 5. A handler that imports app/Repositories skips the service the same way, and
// the compiler cannot see it: the repository method it calls does require a
// Grant, and a handler can produce one with SystemGrant.
//
// Service and Repository are not a convention an organized team chooses to keep:
// they are skeleton, and the direction of the arrow between them is checked.
func controllerMustNotReachTheRepository(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		kind, onTheRequestPath := requestPath(f)
		if f.isTest || !onTheRequestPath {
			continue
		}
		for imported := range f.imports {
			if !strings.Contains(strings.ToLower(imported), "/app/repositories") {
				continue
			}
			out = append(out, Finding{
				Rule: "controller-reaches-repository", Severity: Error,
				File: f.rel, Line: 1,
				Message: "this " + kind + " imports " + imported,
				Why:     "the Grant a repository requires would have to be issued here, which is the request authorizing itself. Call app/Services instead: it validates, calls security.Authorize, and only then reaches the repository.",
			})
			break
		}
	}
	return out
}

// 6. A tenant taken from the request is the client choosing which data to read.
func tenantMustComeFromTheGrant(p *project) []Finding {
	files := p.files
	var out []Finding
	for _, f := range files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			switch name {
			case "r.PathValue", "r.FormValue", "r.PostFormValue", "req.PathValue", "req.FormValue",
				"ctx.Param", "ctx.Query", "ctx.Input", "c.Param", "c.Query", "c.Input":
			default:
				return
			}
			if len(call.Args) == 0 {
				return
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || !strings.Contains(strings.ToLower(lit.Value), "tenant") {
				return
			}
			file, line := f.at(call)
			out = append(out, Finding{
				Rule: "tenant-from-request", Severity: Error,
				File: file, Line: line,
				Message: "the tenant is being read from the request",
				Why:     "a tenant the client controls is the client choosing whose data to read. It comes from data.Tenant(g), and the Grant comes from the session.",
			})
		})

		// The header form, which is the one that looks harmless.
		f.calls(func(call *ast.CallExpr, name string) {
			if !strings.HasSuffix(name, "Header.Get") && name != "r.Header.Get" {
				return
			}
			if len(call.Args) == 0 {
				return
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && strings.Contains(strings.ToLower(lit.Value), "tenant") {
				file, line := f.at(call)
				out = append(out, Finding{
					Rule: "tenant-from-header", Severity: Error,
					File: file, Line: line,
					Message: "the tenant is being read from a header",
					Why:     "any client can send that header. Resolve the tenant from the host name or from a constant, and let it reach SQL only through the Grant.",
				})
			}
		})

		out = append(out, requestValuesReachingAGrant(f)...)
	}
	return out
}

// tenantShape is how a grant constructor spells the tenant it was handed.
type tenantShape int

const (
	// tenantSecondArgument is SystemGrant(action, tenant).
	tenantSecondArgument tenantShape = iota
	// tenantOnAJobLiteral is GrantFor(Job{TenantID: ...}), by value or behind
	// an ampersand.
	tenantOnAJobLiteral
)

// grantConstructor is one exported function that hands back a Grant without a
// Policy having answered for it.
type grantConstructor struct {
	// pkg is the import path of the package declaring it, and the import path
	// is the whole point -- see grantConstructors.
	pkg string
	// name is the function.
	name string
	// tenant is where in the call the tenant is written.
	tenant tenantShape
}

// grantConstructors are the exported functions that hand back a Grant without a
// Policy having answered for it.
//
// Authorize is not here, and that is the point: it IS the policy check, so a
// Grant that came from it was authorized by construction. What is listed is the
// escape hatch and everything that wraps it.
//
// It is a list rather than one name because wrapping is what got past the rules
// below. They matched the literal `security.SystemGrant`, and GrantFor delegates
// to it with the action and the tenant read off a Job -- an ordinary exported
// struct that any package can fill in:
//
//	jobs.GrantFor(jobs.Job{Action: "customer.delete", TenantID: ctx.Query("org")})
//
// That reaches the database with permissions nobody granted, under a tenant the
// caller chose, and a rule matching one literal name produces no finding at all.
//
// Each entry is an import path and a function, and they are matched through the
// file's import block rather than against the identifier written at the call
// site. The identifier is the caller's to choose:
//
//	import hjobs "github.com/arandu-io/hesape/queue/jobs"
//	g := hjobs.GrantFor(&hjobs.Job{Action: "customer.delete", TenantID: org})
//
// A list of strings like "jobs.GrantFor" matches nothing there, and an
// authorization rule that a one-word alias switches off is not one. The same
// spelling also matched by accident: the components moved into hesape under
// packages whose last path element is unchanged, so `jobs.GrantFor` went on
// matching a different module's function for as long as nobody aliased it.
//
// Four entries and two modules, because both spell the same two functions: the
// framework re-exports what hesape declares, and a project can reach either.
//
// A new function returning a Grant has to be added here. Nothing makes that
// automatic -- the doctor reads one package at a time and does not resolve types
// across modules -- so TestEveryGrantConstructorIsGuarded reads both modules'
// source and fails when one of them exports a constructor this list does not
// name.
var grantConstructors = []grantConstructor{
	{"github.com/arandu-io/framework/security", "SystemGrant", tenantSecondArgument},
	{"github.com/arandu-io/hesape/auth", "SystemGrant", tenantSecondArgument},
	{"github.com/arandu-io/framework/jobs", "GrantFor", tenantOnAJobLiteral},
	{"github.com/arandu-io/hesape/queue/jobs", "GrantFor", tenantOnAJobLiteral},
}

// grantConstructor resolves a call to the constructor it names, through this
// file's imports.
//
// The name comes from callName, so it is the whole chain as written. A call on
// a package is exactly two segments -- an identifier that has to be an import,
// and the function -- and anything longer is a method on a value.
func (f *file) grantConstructor(called string) (grantConstructor, bool) {
	local, fn, qualified := strings.Cut(called, ".")
	switch {
	case !qualified:
		// A dot import puts the function in this file's own namespace, so the
		// call site qualifies nothing at all: `SystemGrant(a, tenant)`. Rare,
		// and free to cover: parseProject records the dot as the local name.
		local, fn = ".", called
	case strings.Contains(fn, "."):
		return grantConstructor{}, false
	}

	path, imported := f.importPath(local)
	if !imported {
		return grantConstructor{}, false
	}
	for _, c := range grantConstructors {
		if c.pkg == path && c.name == fn {
			return c, true
		}
	}
	return grantConstructor{}, false
}

// importPath is the path this file imports under a local name.
//
// f.imports is keyed the other way round, by path, because most rules ask what
// a file imports rather than what a name refers to. Two imports cannot share a
// local name in one file, so the scan has one answer.
func (f *file) importPath(local string) (string, bool) {
	for path, name := range f.imports {
		if name == local {
			return path, true
		}
	}
	return "", false
}

// tenantArg returns the expression that supplies the tenant to this call.
//
// The two shapes spell it differently: SystemGrant takes it as the second
// positional argument, and GrantFor takes a Job whose TenantID field carries
// it. Reading only the positional form is what let the wrapper through.
//
// The Job arrives by value in one module and behind an ampersand in the other,
// which is a difference in the two signatures and not in what is being written:
// unwrapping it here is what keeps one rule over both.
func (c grantConstructor) tenantArg(call *ast.CallExpr) (ast.Expr, bool) {
	switch c.tenant {
	case tenantSecondArgument:
		if len(call.Args) >= 2 {
			return call.Args[1], true
		}
	case tenantOnAJobLiteral:
		if len(call.Args) != 1 {
			return nil, false
		}
		arg := call.Args[0]
		if unary, ok := arg.(*ast.UnaryExpr); ok && unary.Op == token.AND {
			arg = unary.X
		}
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			return nil, false
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "TenantID" {
				return kv.Value, true
			}
		}
	}
	return nil, false
}

// requestValuesReachingAGrant follows the value instead of reading the name.
//
// The two checks above ask whether a header or a parameter is CALLED something
// with "tenant" in it, and that is the wrong question:
//
//	org := ctx.Query("org")
//	g := security.SystemGrant(ActionView, org)
//
// passes both of them, and it is exactly the hole that matters. The name of a
// header is chosen by whoever wrote the client; what makes it a tenant is where
// the value ends up.
//
// The analysis is deliberately small: within one function, a variable assigned
// from something the request controls is tainted, and a tainted variable
// reaching the tenant argument of SystemGrant is the finding. It does not follow
// values across functions, and it says so -- a doctor that claims more coverage
// than it has is worse than one that claims less.
func requestValuesReachingAGrant(f *file) []Finding {
	var out []Finding

	ast.Inspect(f.ast, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		// Pass one: every local name assigned from a request-controlled read.
		tainted := map[string]*ast.CallExpr{}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			var lhs []ast.Expr
			var rhs []ast.Expr
			switch a := n.(type) {
			case *ast.AssignStmt:
				lhs, rhs = a.Lhs, a.Rhs
			default:
				return true
			}
			if len(lhs) != len(rhs) {
				return true
			}
			for i, r := range rhs {
				call, ok := r.(*ast.CallExpr)
				if !ok || !readsTheRequest(callName(call)) {
					continue
				}
				if id, ok := lhs[i].(*ast.Ident); ok && id.Name != "_" {
					tainted[id.Name] = call
				}
			}
			return true
		})
		// No early return on an empty `tainted`: the embedded form assigns
		// nothing, and skipping pass two here is what hid it.

		// Pass two: the tenant argument of a grant constructor, either a name
		// pass one tainted or a request read written inline.
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			constructor, isGrant := f.grantConstructor(callName(call))
			if !isGrant {
				return true
			}
			arg, ok := constructor.tenantArg(call)
			if !ok {
				return true
			}

			// The embedded form, with no variable in between:
			//
			//	security.SystemGrant(ActionView, ctx.Query("org"))
			//
			// Pass one never sees it, because nothing was assigned. It is the
			// shortest way to write the hole this rule exists to close, and it
			// went unreported while the longer version was caught.
			if inner, isCall := arg.(*ast.CallExpr); isCall {
				if readsTheRequest(callName(inner)) {
					file, line := f.at(call)
					out = append(out, Finding{
						Rule: "tenant-from-request", Severity: Error,
						File: file, Line: line,
						Message: fmt.Sprintf("the tenant of this Grant is read straight off the request, by %s", callName(inner)),
						Why:     "whoever sent the request picks the tenant, and reads every row of it. It comes from the Grant.",
					})
				}
				return true
			}

			id, ok := arg.(*ast.Ident)
			if !ok {
				return true
			}
			source, isTainted := tainted[id.Name]
			if !isTainted {
				return true
			}
			file, line := f.at(call)
			_, sourceLine := f.at(source)
			out = append(out, Finding{
				Rule: "tenant-from-request", Severity: Error,
				File: file, Line: line,
				Message: fmt.Sprintf("the tenant of this Grant is %s, which line %d read from the request", id.Name, sourceLine),
				Why:     "the name of a header or a parameter is chosen by whoever wrote the client, so it proves nothing. What makes a value a tenant is that it scopes SQL -- and a client that picks its own scope reads whichever customer it names. Resolve the tenant from the session, the host name or a constant.",
			})
			return true
		})
		return true
	})
	return out
}

// readsTheRequest reports whether a call returns something the client controls.
func readsTheRequest(name string) bool {
	switch {
	case strings.HasSuffix(name, "Header.Get"),
		strings.HasSuffix(name, "Query.Get"),
		strings.HasSuffix(name, ".PathValue"),
		strings.HasSuffix(name, ".FormValue"),
		strings.HasSuffix(name, ".PostFormValue"),
		strings.HasSuffix(name, ".Cookie"),
		// The http.Context accessors: the same values, one layer up.
		strings.HasSuffix(name, "ctx.Param"),
		strings.HasSuffix(name, "ctx.Query"),
		strings.HasSuffix(name, "ctx.Input"):
		return true
	}
	return false
}

// 7. SystemGrant is the one way past the policy. Its call sites are the audit.
//
// Two things can excuse a call, and the name of the enclosing function is not
// one of them. Letting through any function whose name contains ensure, seed,
// job, worker, migrate or backfill passes `ensureGrant` and fires on
// `issueGrant`, identical in every other character. A check a rename defeats is
// a spelling convention.
//
// What excuses a call:
//
//   - the file is one of the places the skeleton puts work with no request
//     behind it -- database/seeders, app/Jobs, app/Console, cmd, main.go. That
//     is a directory the framework decided, not a word somebody chose;
//   - the line carries `//arandu:system-grant <reason>`. An escape has to be
//     deliberate and visible: it stays in the diff, it is read in review, and
//     the reason is written down next to the thing it excuses. A marker with no
//     reason excuses nothing.
func systemGrantIsAudited(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		systemScope := inASystemScope(f.rel)
		escapes := systemGrantEscapes(f)
		enclosing := enclosingFuncs(f)

		f.calls(func(call *ast.CallExpr, name string) {
			constructor, isGrant := f.grantConstructor(name)
			if !isGrant {
				return
			}
			file, line := f.at(call)

			// An empty tenant is refused by the framework at runtime; catching it
			// here says so before anyone waits for the failure.
			//
			// The message names the constructor that was called rather than
			// SystemGrant always. GrantFor reaches here too, and telling
			// somebody their SystemGrant is wrong when they wrote no such call
			// sends them looking for a line that is not in the file.
			if arg, found := constructor.tenantArg(call); found {
				if lit, ok := arg.(*ast.BasicLit); ok && (lit.Value == `""` || lit.Value == "``") {
					out = append(out, Finding{
						Rule: "system-grant-without-tenant", Severity: Error,
						File: file, Line: line,
						Message: constructor.name + " with an empty tenant",
						Why:     "it returns the zero Grant, which fails Check -- so this call site is dead code. A system grant with no tenant would read across every customer, which is why it does not exist.",
					})
					return
				}
			}
			if systemScope || escapes[line] || escapes[line-1] {
				return
			}

			where := constructor.name
			if fn := enclosing[line]; fn != "" {
				where = constructor.name + " in " + fn
			}
			out = append(out, Finding{
				Rule: "system-grant-outside-scope", Severity: Warning,
				File: file, Line: line,
				Message: where + " is outside a seeder, a job and a command",
				Why:     "it is the one way past a policy, and its call sites are the audit. If this is a request path, the Grant should come from security.Authorize instead. If the escape is deliberate, write `//arandu:system-grant <reason>` on the line, so the reason is read in review rather than inferred from the function name.",
			})
		})
	}
	return out
}

// inASystemScope reports whether the file is one of the places the skeleton puts
// work that has no request behind it.
//
// By path segment, not by substring. "seeder" anywhere in a path matched
// app/Services/SeederService.go, which is the same rename hole one level up.
func inASystemScope(rel string) bool {
	lower := strings.ToLower(filepath.ToSlash(rel))
	if lower == "main.go" || lower == "routes/console.go" {
		return true
	}
	for _, segment := range strings.Split(path.Dir(lower), "/") {
		switch segment {
		case "seeders", "jobs", "commands", "console", "cmd":
			return true
		}
	}
	return false
}

// systemGrantEscapes returns the lines carrying a deliberate escape.
//
// The marker is `//arandu:system-grant <reason>`, on the line of the call or on
// the line above it, and the reason is required: a bare marker is a suppression
// with nothing to review, which is what this rule exists to prevent.
func systemGrantEscapes(f *file) map[int]bool {
	out := map[int]bool{}
	for _, group := range f.ast.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			reason, marked := strings.CutPrefix(text, "arandu:system-grant")
			if !marked || strings.TrimSpace(reason) == "" {
				continue
			}
			out[f.fset.Position(c.Pos()).Line] = true
		}
	}
	return out
}

// enclosingFuncs maps every line of the file to the function that contains it,
// so a finding can be judged by where it sits.
//
// A function literal held by a package-level var answers with the name of the
// var. It is what the reader has to go and open, and it is the only name the
// code gives it -- without this a finding inside `var Export = func(...)` would
// name no function at all, which is the one thing a report may not do when the
// rule it comes from reads bodies wherever they are written.
func enclosingFuncs(f *file) map[int]string {
	out := map[int]string{}
	mark := func(node ast.Node, name string) {
		from := f.fset.Position(node.Pos()).Line
		to := f.fset.Position(node.End()).Line
		for l := from; l <= to; l++ {
			out[l] = name
		}
	}
	for _, decl := range f.ast.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				mark(d, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// Paired by position, so `var a, b = func(){}, func(){}` names
				// each half rather than calling both of them a.
				for i, value := range vs.Values {
					if i >= len(vs.Names) || !holdsAFunc(value) {
						continue
					}
					if name := vs.Names[i].Name; name != "" && name != "_" {
						mark(value, name)
					}
				}
			}
		}
	}
	return out
}

// holdsAFunc reports whether the expression contains a function literal, at any
// depth: a bare `func(){}`, and one inside the struct or slice a var is built
// from.
func holdsAFunc(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			found = true
		}
		return !found
	})
	return found
}

// 8. SQL built with Sprintf or concatenation of a variable is injection, whatever
// the intent was.
func noBuiltSQL(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}

		f.calls(func(call *ast.CallExpr, name string) {
			if name != "fmt.Sprintf" || len(call.Args) == 0 {
				return
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || !looksLikeSQL(lit.Value) {
				return
			}
			file, line := f.at(call)
			out = append(out, Finding{
				Rule: "sql-built-with-sprintf", Severity: Error,
				File: file, Line: line,
				Message: "SQL assembled with fmt.Sprintf",
				Why:     "every value interpolated into SQL is injection waiting for the right input. Use ? placeholders and pass the values as arguments -- the dialect rebinds them.",
			})
		})

		out = append(out, concatenatedSQL(f)...)
	}
	return out
}

// concatenatedSQL is the other half of the check.
//
// Reading fmt.Sprintf alone leaves a hole in the one barrier against hand-built
// SQL:
//
//	"SELECT id FROM invoices WHERE reference LIKE '%" + term + "%'"
//
// passes clean, with term coming straight off the request.
//
// # What separates it from the concatenation the generator writes on purpose
//
// The generated repository concatenates too, and correctly: a package-level
// const for the column list, and a `column` local that was chosen from an
// allowlist. Neither can carry a value from outside.
//
// The signal that tells them apart, with no type information, is where the
// operand comes from: a PARAMETER of the enclosing function, or a field of one,
// is a value the caller supplied. A const or a local is not.
//
// # The limit, stated rather than implied
//
// Assigning a parameter to a local and concatenating the local is not caught --
// separating that from the allowlist pattern needs dataflow, and the allowlist
// pattern is what the generator emits. A rule that flagged it would fire on the
// code the generator writes, and a rule that fires on correct code is how a tool
// teaches people to ignore it.
func concatenatedSQL(f *file) []Finding {
	var out []Finding

	f.functions(func(fn *ast.FuncDecl) {
		params := parameterNames(fn)
		if len(params) == 0 || fn.Body == nil {
			return
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				return true
			}

			var sawSQL bool
			var offender string
			var walk func(ast.Expr)
			walk = func(e ast.Expr) {
				switch v := e.(type) {
				case *ast.BinaryExpr:
					if v.Op == token.ADD {
						walk(v.X)
						walk(v.Y)
					}
				case *ast.BasicLit:
					if v.Kind == token.STRING && looksLikeSQL(v.Value) {
						sawSQL = true
					}
				case *ast.Ident:
					if params[v.Name] {
						offender = v.Name
					}
				case *ast.SelectorExpr:
					// q.Sort, in.Term: a field of a parameter is still the
					// caller's value.
					if root, ok := v.X.(*ast.Ident); ok && params[root.Name] {
						offender = root.Name + "." + v.Sel.Name
					}
				}
			}
			walk(bin)

			if sawSQL && offender != "" {
				file, line := f.at(bin)
				out = append(out, Finding{
					Rule: "sql-built-by-concatenation", Severity: Error,
					File: file, Line: line,
					Message: "SQL assembled by concatenating " + offender,
					Why: "a value the caller supplied reaches the statement as text, which is injection with one more step than fmt.Sprintf. Use a ? placeholder and pass it as an argument. " +
						"A column or table NAME cannot be a placeholder -- pick it from an allowlist first, and concatenate the allowlisted value, which is what `aru make:module` emits.",
				})
				return false
			}
			return true
		})
	})
	return out
}

// parameterNames is the set of names the function's signature binds.
func parameterNames(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Type == nil || fn.Type.Params == nil {
		return out
	}
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				out[name.Name] = true
			}
		}
	}
	return out
}

func looksLikeSQL(s string) bool {
	upper := strings.ToUpper(s)
	for _, kw := range []string{"SELECT ", "INSERT ", "UPDATE ", "DELETE ", " FROM ", " WHERE "} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// 9. A type with a secret in it needs to refuse to serialize itself, or the
// first Dump publishes it on the debug page.
func sensitiveFieldNeedsRedaction(p *project) []Finding {
	sensitive := []string{"password", "secret", "token", "document", "apikey", "api_key", "creditcard", "cpf", "cnpj"}

	var out []Finding
	for _, f := range p.files {
		// Anything under app/ -- a model, a request, a DTO. Outside it the
		// project is configuration and wiring, where a field called Token is the
		// credential being read rather than a record being stored.
		if f.isTest || f.category == "" {
			continue
		}
		f.types(func(ts *ast.TypeSpec) {
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return
			}

			var found string
			for _, field := range st.Fields.List {
				for _, n := range field.Names {
					lower := strings.ToLower(n.Name)
					for _, s := range sensitive {
						if strings.Contains(lower, s) {
							found = n.Name
						}
					}
				}
			}
			if found == "" || hasRedaction(p.files, f.dir, ts.Name.Name) {
				return
			}
			file, line := f.at(ts)
			out = append(out, Finding{
				Rule: "sensitive-field-not-redacted", Severity: Warning,
				File: file, Line: line,
				Message: ts.Name.Name + " holds " + found + " and does not redact itself",
				Why:     "one observability.Dump or one log line publishes it on the debug page. Add LogValue() slog.Value and MarshalJSON to the type, so no caller has to remember.",
			})
		})
	}
	return out
}

// hasRedaction looks for the methods in the directory the type lives in.
//
// The directory, not the whole project: a method has to be declared in the
// package of its receiver, so anywhere else is not the same type. That is also
// what makes the check right in this tree, where app/Models and
// app/Requests are two packages that both declare a type called User.
func hasRedaction(files []*file, dir, typeName string) bool {
	for _, f := range files {
		if f.dir != dir {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || receiverType(fn) != typeName {
				continue
			}
			if fn.Name.Name == "LogValue" || fn.Name.Name == "MarshalJSON" {
				return true
			}
		}
	}
	return false
}

// sessionRotationCalls are the method names that end a login with the record
// the request arrived with destroyed.
//
// It is a list rather than two literals in the matcher because a name here is
// a claim about somebody else's API, and a claim nobody reads back goes stale
// silently. This one had gone wrong in both directions at once: it accepted
// Start, which exists and does not close the hole, and it did not carry
// Regenerate, so a login written the way the session package documents was
// reported as the violation. A test resolves these against the modules that
// declare them.
var sessionRotationCalls = []string{"Regenerate", "Rotate"}

// 10. Login without rotating the session id is session fixation: an attacker
// plants a known id and inherits the session after the victim signs in.
//
// Two names close it, because the operation is called two things one layer
// apart: SessionStore.Rotate is the framework's name, and Regenerate is the
// name underneath it and the one a project that reaches the session package
// directly writes. Both destroy the record the request arrived with, and the
// rule takes either.
//
// Start is refused, and that is the whole point of the check rather than an
// omission from the list. Start has no parameter for the old id, so a login
// body holding only Start cannot destroy the planted record -- the destruction
// is not skipped, it is unsayable. And refusing it costs nothing: Regenerate
// given an empty old id does exactly what Start does, so the login that arrived
// with no session is already written correctly as Regenerate. There is no
// sign-in shape that needs Start, and accepting it passed a login that leaves
// the planted session readable.
func sessionMustRotateOnLogin(p *project) []Finding {
	accepts := func(name string) bool {
		for _, call := range sessionRotationCalls {
			if strings.HasSuffix(name, "."+call) {
				return true
			}
		}
		return false
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := strings.ToLower(fn.Name.Name)
			if !strings.Contains(name, "login") && !strings.Contains(name, "signin") {
				continue
			}
			if !funcBodyContains(fn, func(n string) bool { return strings.Contains(n, "Authenticate") }) {
				continue
			}
			if funcBodyContains(fn, accepts) {
				continue
			}
			file, line := f.at(fn)
			out = append(out, Finding{
				Rule: "session-not-rotated", Severity: Error,
				File: file, Line: line,
				Message: fn.Name.Name + " authenticates and does not rotate the session",
				Why:     "keeping the pre-login id is session fixation: an attacker plants a known id and inherits the session once the victim signs in. After Authenticate, call Regenerate with the id the request arrived with -- SessionStore spells the same call Rotate. Start is not enough: it takes no old id, so it mints a new one and leaves the planted record readable.",
			})
		}
	}
	return out
}

// renderCall is a ctx.View or ctx.Fragment call, with the two arguments the view
// rules care about.
type renderCall struct {
	call *ast.CallExpr
	// name is the view name argument, whatever it is written as.
	name ast.Expr
	// data is the argument that reaches the template.
	data ast.Expr
}

// renderCalls finds the calls that render a view.
//
// It matches by shape rather than by type, because doctor never type-checks:
// `ctx.View(name, data)` has two arguments and `ctx.Fragment(status, name,
// data)` has three, and the method names come from http.Context. A method
// called View with two arguments on something else is a false positive this
// accepts -- and the alternative, type resolution, would mean doctor could only
// run on a project that already compiles.
func renderCalls(f *file) []renderCall {
	var out []renderCall
	f.calls(func(call *ast.CallExpr, name string) {
		switch {
		case strings.HasSuffix(name, ".View") && len(call.Args) == 2:
			out = append(out, renderCall{call: call, name: call.Args[0], data: call.Args[1]})
		case strings.HasSuffix(name, ".Fragment") && len(call.Args) == 3:
			out = append(out, renderCall{call: call, name: call.Args[1], data: call.Args[2]})
		}
	})
	return out
}

// 11. The data of a view is a typed struct, never a map.
//
// It is the reason the view layer is typed at all: with a struct, a
// renamed field is a compile error and the page cannot ship broken. With
// map[string]any, a typo in a key is a nil the template renders as nothing --
// the page comes up, the field is blank, and nobody finds out until a customer
// says the invoice has no total.
//
// It follows the value one step, because the map is usually built on the line
// above the render rather than inside the call.
func viewDataMustBeAStruct(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		maps := mapVariables(f)

		for _, r := range renderCalls(f) {
			source, why := "", ""
			switch {
			case isMapExpression(r.data):
				why = "this call passes a map"
			default:
				id, ok := r.data.(*ast.Ident)
				if !ok {
					continue
				}
				at, isMap := maps[id.Name]
				if !isMap {
					continue
				}
				source = id.Name
				why = fmt.Sprintf("%s, built on line %d, is a map", source, f.line(at))
			}

			file, line := f.at(r.call)
			out = append(out, Finding{
				Rule: "view-data-is-a-map", Severity: Error,
				File: file, Line: line,
				Message: "the data of " + viewNameOf(r) + " is a map: " + why,
				Why:     "a struct is what makes a typo a compile error. With a map, a renamed or misspelled key renders as an empty string, the page comes up, and the missing field is found in production. Declare the type in the view's @go block and pass it.",
			})
		}
	}
	return out
}

// viewNameOf renders the view being rendered, for a message.
func viewNameOf(r renderCall) string {
	if name, ok := stringLiteral(r.name); ok {
		return "view " + name
	}
	return "this view"
}

// isMapExpression reports whether an expression is plainly a map.
func isMapExpression(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.CompositeLit:
		_, ok := x.Type.(*ast.MapType)
		return ok
	case *ast.CallExpr:
		// make(map[string]any)
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "make" && len(x.Args) > 0 {
			_, isMap := x.Args[0].(*ast.MapType)
			return isMap
		}
	case *ast.UnaryExpr:
		return isMapExpression(x.X)
	}
	return false
}

// mapVariables collects the local names that hold a map, per file.
//
// One step of assignment, no more: the shape people write is a literal on the
// line before the render, and an analysis that promised to follow a value
// through three functions without a type checker would be promising something it
// cannot deliver.
func mapVariables(f *file) map[string]ast.Node {
	out := map[string]ast.Node{}

	ast.Inspect(f.ast, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, rhs := range x.Rhs {
				if !isMapExpression(rhs) {
					continue
				}
				if id, ok := x.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
					out[id.Name] = rhs
				}
			}
		case *ast.ValueSpec:
			if _, isMap := x.Type.(*ast.MapType); isMap {
				for _, id := range x.Names {
					out[id.Name] = x
				}
				return true
			}
			for i, value := range x.Values {
				if i < len(x.Names) && isMapExpression(value) {
					out[x.Names[i].Name] = value
				}
			}
		}
		return true
	})
	return out
}

// 12. A view that is rendered has to exist.
//
// The name is a string, so this is the one place in the view path the compiler
// cannot reach: `ctx.View("invoices.idex", data)` compiles, deploys, and returns
// 500 the first time somebody opens the page. Nothing else in the request would
// have failed.
//
// It stays quiet on a project with no views at all -- a library, or an
// application whose views have not been written yet -- because a rule that fires
// on every call in an empty project teaches people to ignore it.
func viewMustExist(p *project) []Finding {
	if len(p.views) == 0 {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, r := range renderCalls(f) {
			name, ok := stringLiteral(r.name)
			if !ok || name == "" {
				// A name computed at runtime is outside what this can check, and
				// saying so is better than guessing.
				continue
			}
			if p.hasView(name) {
				continue
			}
			file, line := f.at(r.call)
			out = append(out, Finding{
				Rule: "view-does-not-exist", Severity: Error,
				File: file, Line: line,
				Message: "there is no view named " + name,
				Why:     "the name of a view is a string, so this is the one thing in the view path the compiler cannot check: it builds, it deploys, and the page answers 500 the first time somebody opens it. Create " + viewsDir + "/" + strings.ReplaceAll(name, ".", "/") + ".kyse.go, or fix the name.",
			})
		}
	}
	return out
}

// 13. What the project declares in arandu.mod.toml has to match what it does.
//
// The declaration is the only thing anyone reads before installing a module from
// the registry, and a declaration nobody verifies is worse than none: it is a
// promise with the weight of a check and the reliability of a comment.
//
// Used and not declared is an error -- that is the code doing something its
// installer did not agree to. Declared and not used is a warning, because asking
// for more than you need is how a permission model erodes into everyone
// declaring everything.
//
// A project with no manifest is silent. The unit of distribution is the Go
// module, so the file belongs at the root of a repository that is published --
// and an application is not published. Demanding it from every
// `aru new` would be a warning that fires on correct code, which is how a tool
// teaches people to stop reading it.
func declaredPermissionsMatchTheCode(p *project) []Finding {
	declared := p.manifest
	if declared == nil {
		return nil
	}

	name := declared.Name
	if name == "" {
		name = "this project"
	}
	used := usedPermissions(p)

	var out []Finding
	for _, c := range []struct {
		name        string
		declared    bool
		used        bool
		consequence string
	}{
		{"network", declared.Permissions.Network, used.Network,
			"the code makes calls that leave the process, and whoever installed it agreed to a module that does not"},
		{"filesystem", declared.Permissions.Filesystem, used.Filesystem,
			"the code reads or writes files outside the database, which is not visible from its API"},
		{"exec", declared.Permissions.Exec, used.Exec,
			"the code runs another program, which is the widest capability there is"},
		{"migrations", declared.Permissions.Migrations, used.Migrations,
			"the code owns tables, and an installer who did not expect that will not know to run aru migrate"},
	} {
		switch {
		case c.used && !c.declared:
			out = append(out, Finding{
				Rule: "permission-not-declared", Severity: Error,
				File: relativeTo(p.root, declared.Path), Line: 1,
				Message: name + " uses " + c.name + " and declares " + c.name + " = false",
				Why:     c.consequence + ". Set " + c.name + " = true under [permissions], or remove the code that needs it.",
			})
		case c.declared && !c.used && len(p.unreadable) == 0:
			// Absence, so it needs the guard, and here the guard is the whole
			// file set: a project whose only network call sits in a file that
			// does not parse would be told to declare network = false, which is
			// the opposite of true.
			out = append(out, Finding{
				Rule: "permission-not-used", Severity: Warning,
				File: relativeTo(p.root, declared.Path), Line: 1,
				Message: name + " declares " + c.name + " = true and does not use it",
				Why:     "asking for more than you need is how a permission model erodes into everyone declaring everything. Set " + c.name + " = false.",
			})
		}
	}
	return out
}

// usedPermissions is the capability audit, by AST.
//
// It looks at what the code calls rather than at what it imports, because
// net/http is imported by every project that has a controller and says nothing
// about whether it calls out.
func usedPermissions(p *project) manifest.Permissions {
	var used manifest.Permissions

	for _, f := range p.files {
		if f.isTest {
			continue
		}

		// database/migrations is where the schema lives, so
		// its existence is the declaration -- no method name to remember.
		if strings.HasPrefix(f.rel, "database/migrations/") {
			used.Migrations = true
		}

		for path := range f.imports {
			switch path {
			case "os/exec":
				used.Exec = true
			case "net/smtp", "net/rpc":
				used.Network = true
			case "io/ioutil":
				used.Filesystem = true
			}
		}

		f.calls(func(_ *ast.CallExpr, name string) {
			switch {
			// The client half of net/http. The server half -- Handler,
			// ResponseWriter, StatusOK -- is every project with a route.
			case name == "http.Get", name == "http.Post", name == "http.Head",
				name == "http.PostForm", name == "http.NewRequest",
				name == "http.NewRequestWithContext",
				strings.HasSuffix(name, ".Do"),
				name == "net.Dial", name == "net.DialTimeout":
				used.Network = true

			case name == "os.Open", name == "os.OpenFile", name == "os.Create",
				name == "os.ReadFile", name == "os.WriteFile", name == "os.Remove",
				name == "os.RemoveAll", name == "os.Mkdir", name == "os.MkdirAll",
				name == "os.Rename", name == "os.ReadDir",
				name == "filepath.Walk", name == "filepath.WalkDir":
				used.Filesystem = true

			case name == "exec.Command", name == "exec.CommandContext", name == "syscall.Exec":
				used.Exec = true
			}
		})

		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv != nil && fn.Name.Name == "Migrations" {
				used.Migrations = true
			}
		}
	}
	return used
}

func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// clientDirective captures an attribute that holds state or behaviour in the
// browser, and the value it holds.
//
// These are written in HTML attributes, so this is text matching rather than
// parsing. That is a real limitation: a directive split across lines by a
// formatter still matches, one built by string concatenation in Go does not, and
// a `@go` block that happens to contain the shape in a string literal matches
// like markup would. It catches the shape people actually write.
//
// The name is any `x-` attribute rather than a written-out list. A list would
// hold the dozen directives of the core and miss x-mask, x-intersect, x-collapse
// and every other one a plugin adds -- and a list that has to be extended per
// plugin is a list that goes stale, which is the failure this rule was corrected
// for. Nothing else claims the prefix: HTML gives custom attributes `data-`, and
// the other library on the page spells its own `hx-`.
//
// The `@name` shorthand is the same attribute in its short spelling, and it
// cannot collide with the template's own directives: those take arguments in
// parentheses that end the line, so `@name="value"` is never one of them. The
// character before the name is what separates the families -- without it `hx-get`
// reads as `x-get` and `data-x-id` as `x-id`, and the difference between an
// attribute that routes a swap and one that holds a value is this rule's whole
// subject.
//
// Both quote styles, because a formatter that prefers single quotes turned the
// whole rule off once already.
var clientDirective = regexp.MustCompile(`(?s)(?:^|[^\w:@-])(x-[\w.:-]+|@[\w.:-]+)\s*=\s*("[^"]*"|'[^']*')`)

// reachesTheServer is what makes a directive more expensive than an inert one.
//
// It no longer decides whether there is a finding -- every directive here is one
// -- and it stays because it decides what the finding says. A dropdown somebody
// wrote in the wrong stack and a second fetch path with its own CSRF handling
// both have to be reported, and telling them apart in the message is the
// difference between a person reaching for ui.js and a person reaching for an
// HTMX fragment.
var reachesTheServer = []struct {
	token string
	what  string
}{
	{"fetch(", "calls fetch()"},
	{"axios", "calls axios"},
	{"XMLHttpRequest", "opens an XMLHttpRequest"},
	{"$store", "reads a global store"},
	{"navigator.sendBeacon", "sends a beacon"},
	{"EventSource(", "opens a server-sent events subscription"},
	{"new WebSocket", "opens a WebSocket"},
}

// 14. A view keeps no state in the browser.
//
// State on this stack is the server's. A handler reads the form, decides, and
// answers markup that is already correct, so there is no second copy in the
// browser to keep in step and nothing to reconcile when the two disagree. An
// `hx-` attribute is not a counter-example and cannot become one: hx-post,
// hx-target and hx-swap say where to ask and what to replace, and nothing reads
// a value back out of one.
//
// What the browser does own is what dies with the tab and the server never needs
// to hear about -- a menu that is open, a row that is selected -- and it has a
// home already: the behaviours file the layout loads. It binds on document and
// dispatches on `data-` attributes, keeps open and selected in the ARIA the
// markup already carries, and evaluates nothing, so the DOM is the only copy and
// swapped-in markup is live where it lands.
//
// # Why this refuses the directive rather than what is inside it
//
// This rule used to permit an `x-` attribute and refuse only the ones whose value
// called the network, and in that shape it was the weaker half of a line the rest
// of the collection draws whole. Of four directives planted in a generated
// project -- a state object, a shorthand handler, a visibility toggle and one
// fetch -- it reported the fetch and passed the other three, which are exactly
// the three the starter kit's own gate fails on. The narrow rule had become the
// permissive one.
//
// What decided the widening is that a generated project cannot run one. The
// layout links a stylesheet, HTMX, the theme script and the behaviours file;
// asking the asset table for a directive framework panics rather than answering a
// URL that would 404, so the ordinary way to add the script tag takes the page
// down at render. And the policy the skeleton wires is script-src 'self' with no
// unsafe-eval, while a directive framework compiles every expression it is given
// out of a string -- so even served, not one directive would evaluate.
//
// So an `x-` attribute in a view today is in one of two states, and both are
// worth a finding:
//
//   - inert, which is the ordinary case. Somebody wrote behaviour that never
//     runs, the screen does nothing, and nothing anywhere says why. Silence is
//     the wrong answer to a feature that is not there.
//   - live, which costs more. It is live only because the project deleted the
//     security headers from its own bootstrap and vendored a second client
//     framework, and the application now has two state models, two loading
//     states and two places to forget the CSRF token.
//
// The compiler that builds these views classifies the same attributes as code
// when it escapes an interpolation into one, and that is not a contradiction to
// read as permission: an escaper has to assume the worst about a value a browser
// might evaluate, and a check has to say the value should not be there at all.
//
// # What it does not reach
//
// No view in this repository matches, so a clean run says nothing about whether
// the rule works. What answers for that is the fixture corpus, which plants the
// state directive and the event handler in both quote styles, the inert
// client-only directive that is now a finding, and the `data-` shape that has to
// stay silent.
func viewsKeepNoStateInTheBrowser(p *project) []Finding {
	var out []Finding

	for _, v := range p.views {
		for _, match := range clientDirective.FindAllStringSubmatchIndex(v.body, -1) {
			directive := v.body[match[2]:match[3]]
			// The quotes are part of the capture, so that one alternative can
			// hold both styles; the content is what sits between them.
			quoted := v.body[match[4]:match[5]]
			content := quoted[1 : len(quoted)-1]

			message := directive + " holds state in the browser"
			for _, reach := range reachesTheServer {
				if strings.Contains(content, reach.token) {
					message += ", and its value " + reach.what
					break
				}
			}

			out = append(out, Finding{
				Rule: "view-keeps-state-in-the-browser", Severity: Error,
				// The directive, not the whole match: the match starts one
				// character earlier, and that character can be the newline
				// that ends the line above.
				File: v.rel, Line: lineOf(v.body, match[2]),
				Message: message,
				Why: "Nothing this project serves evaluates that attribute: the layout loads a behaviours file that " +
					"dispatches on data- attributes and evaluates no expression, and the policy is script-src 'self' " +
					"with no unsafe-eval, so a framework that compiles a directive out of a string cannot run beside it. " +
					"Either the directive is inert and the screen silently does nothing, or the policy was relaxed to run " +
					"a second client stack -- and then there are two state models, two loading states and two places to " +
					"forget the CSRF token. Put the decision in a handler and swap the markup; what dies with the tab goes " +
					"on a data- attribute, or in <details> and :focus-within.",
			})
		}
	}
	return out
}

// lineOf returns the 1-indexed line containing the byte offset.
func lineOf(body string, offset int) int {
	if offset > len(body) {
		offset = len(body)
	}
	return strings.Count(body[:offset], "\n") + 1
}

// 15. A statement against a multi-tenant table with no tenant in its predicate
// reads and writes every customer's rows.
//
// It is the leak in its most direct form, and the reason it survives review is
// that everything else about the method is right: the Grant is taken, the Grant
// is checked, the policy exists -- and the `AND tenant_id = ?` somebody deleted
// while debugging never came back. The tenant comes from the Grant, and it has
// to reach the WHERE.
//
// # What decides is the table, not the type holding the handle
//
// This rule used to start by asking whether the receiver was a repository, and
// that made it silent everywhere else. The same SELECT, moved into a type called
// InvoiceService, read every tenant and produced no finding -- not from this rule
// and not from any other, because the rules that guard the request path stop at
// controllers, middleware and routes/. The blind spot sat exactly where rule 4
// sends people: its own sentence says to move the call into app/Services.
//
// So every function is read, method or not, wherever it was written, and what
// answers is whether the statement names a table the project itself shows to be
// multi-tenant. See multiTenantTables.
//
// Wherever it was written includes a function that was never declared. A body
// held by a package-level var runs like any other and reaches the same table:
//
//	var Export = func(ctx context.Context, db *sql.DB) error {
//		_, err := db.QueryContext(ctx, "SELECT total FROM invoices")
//		return err
//	}
//
// Reading declarations alone left that silent, and the finding named no function
// because there was none to name -- the var is what the reader opens, so it is
// what the message says. See file.functionBodies and enclosingFuncs.
//
// What is still outside: a statement written entirely in a package-level const.
// The reader takes bodies, and a const is not one.
//
// INSERT is not checked here. The tenant of a row being written is a value in
// the column list, not a predicate, and it comes from data.Tenant(g) -- a
// different mistake, with a different shape.
//
// # Two escapes, and no third
//
// A migration carries no Grant at all: it runs once per database, from the
// pipeline, with no request behind it, so data.Tenant is not missing there -- it
// has nothing to read. A backfill that sets a default on every row is what the
// directory is for.
//
// Anywhere else the escape is written on the line, `//arandu:system-grant
// <reason>`, which is the marker rule 7 already uses. One mechanism, so an
// escape is deliberate, stays in the diff and is read in review.
func tenantMustScopeTheSQL(p *project) []Finding {
	multiTenant := multiTenantTables(p)
	if len(multiTenant) == 0 {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest || inAMigration(f.rel) {
			continue
		}
		escapes := systemGrantEscapes(f)
		enclosing := enclosingFuncs(f)

		f.functionBodies(func(body *ast.BlockStmt) {
			for _, sql := range sqlStatementsIn(body, scopedVerb) {
				if tenantIsInThePredicate(sql.text) {
					continue
				}
				table, scoped := firstMultiTenantTable(sql.text, multiTenant)
				if !scoped {
					continue
				}
				file, line := f.at(sql.node)
				if escapes[line] || escapes[line-1] {
					continue
				}
				where := sql.verb
				if fn := enclosing[line]; fn != "" {
					where += " in " + fn
				}
				out = append(out, Finding{
					Rule: "sql-without-tenant-scope", Severity: Error,
					File: file, Line: line,
					Message: "the " + where + " reaches " + table + " and does not filter by tenant_id",
					Why: "other statements on " + table + " in this project scope it by tenant, so the table has the column and this one reaches every customer's rows -- reading them, or writing them. " +
						"Add `AND tenant_id = ?` to the WHERE and pass data.Tenant(g), which is the only source of a tenant for SQL. " +
						"If this statement is meant to cross tenants, write `//arandu:system-grant <reason>` on the line, so the reason is read in review.",
				})
			}
		})
	}
	return out
}

// inAMigration reports whether the file is a migration.
//
// By path segment, for the reason inASystemScope is: a substring match would
// take app/Services/MigrationsService.go for a migration, which is the rename
// hole one level up.
func inAMigration(rel string) bool {
	for _, segment := range strings.Split(path.Dir(strings.ToLower(filepath.ToSlash(rel))), "/") {
		if segment == "migrations" {
			return true
		}
	}
	return false
}

// firstMultiTenantTable returns the first table of a statement that is known to
// carry the tenant column.
//
// The first and not all of them: one statement is one finding, and the person
// reading it goes and looks at one WHERE.
func firstMultiTenantTable(text string, multiTenant map[string]bool) (string, bool) {
	for _, table := range tablesNamed(text) {
		if multiTenant[table] {
			return table, true
		}
	}
	return "", false
}

// multiTenantTables reports which tables hold a tenant column, keyed by table
// name.
//
// Nothing in the AST says a table is multi-tenant -- there is no type to inspect
// and doctor never type-checks -- so it is inferred from three things the code
// does say, any one of which is enough:
//
//   - a statement names tenant_id, so the tables it reads have the column;
//   - a repository method calls data.Tenant, which is the only source of a
//     tenant for SQL, so the tables it names are scoped by one;
//   - the entity the repository is named after has a TenantID field.
//
// The third is what catches the worst case: a repository whose every query lost
// the predicate, where the first two signals are gone with it.
//
// # Why the table and not the receiver
//
// Keyed by receiver type, the answer existed only for repositories, so the rule
// above could be widened to read a statement anywhere and would still have known
// nothing outside app/Repositories -- a rule that runs everywhere and answers in
// one directory. The table is what the SQL names, so it is an answer wherever
// the SQL was written.
//
// A repository reaches its tables through its own statements, so no table name is
// ever derived from an entity name: `Invoice` is not turned into `invoices` here,
// and a project that pluralizes differently reads correctly.
//
// # The limit, stated rather than implied
//
// A join is read as evidence about every table it names, so
// `FROM invoices JOIN customers ... WHERE i.tenant_id = ?` marks customers too.
// Both sides of a join inside one repository are one aggregate in every tree the
// generator emits, and on the performance profile the join is already a finding
// of its own -- so the alternative is a narrower signal that costs coverage of
// the column on the joined table and buys nothing back.
func multiTenantTables(p *project) map[string]bool {
	entities := map[string]bool{}
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.types(func(ts *ast.TypeSpec) {
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return
			}
			for _, field := range st.Fields.List {
				for _, n := range field.Names {
					if strings.EqualFold(n.Name, "TenantID") {
						entities[ts.Name.Name] = true
					}
				}
			}
		})
	}

	out := map[string]bool{}
	mark := func(text string) {
		for _, table := range tablesNamed(text) {
			out[table] = true
		}
	}

	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Body == nil {
				return
			}
			// INSERT counts as evidence and is not checked by the rule above:
			// listing tenant_id among the columns says the table holds it just as
			// well as a WHERE does.
			statements := sqlStatementsIn(fn.Body, statementVerb)
			if len(statements) == 0 {
				return
			}

			for _, sql := range statements {
				if strings.Contains(strings.ToLower(sql.text), "tenant_id") {
					mark(sql.text)
				}
			}

			// The two signals that need a repository to be read: one is the
			// entity it is named after, and the other is the tenant it takes off
			// the Grant.
			if fn.Recv == nil || !isRepository(f, fn) {
				return
			}
			entity := strings.TrimSuffix(strings.TrimSuffix(receiverType(fn), "Repository"), "Repo")
			if entity == "" {
				return
			}
			if !entities[entity] && !funcBodyContains(fn, func(n string) bool { return n == "data.Tenant" }) {
				return
			}
			for _, sql := range statements {
				mark(sql.text)
			}
		})
	}
	return out
}

// outboxWriters are imported functions that put a domain event in the outbox,
// and therefore cannot commit without the table. Import identity matters: the
// application aliases framework/events as frameevents, while a local value
// called events must not become a writer by spelling alone.
//
// The value is the sentence that says what stops working, because the two are
// not the same failure to the person reading the report: one is a module of the
// framework that publishes on its own, and the other is the application's own
// code choosing to.
// auth.NewService is in the list and auth.New is too, and they are not ranked
// the same. NewService is where the Outbox is actually
// constructed, from the repository's handle; New is the registration and builds
// nothing, taking a service somebody else made.
//
// The rule began with the registration alone, and on the reference application
// that put the finding in the wrong file: examples/ wraps auth.New inside
// app/Http/Controllers/Auth/LoginController.go, so the report pointed at a
// controller while its own sentence said to add a line to bootstrap/app.go. A
// finding that names one file and asks for a change in another is one the reader
// has to re-derive, and the file it named is not even wrong -- it is just not
// where anything is missing.
type outboxFunction struct {
	display string
	breaks  string
	rank    int
}

type importedFunction struct {
	pkg  string
	name string
}

var outboxWriters = map[importedFunction]outboxFunction{
	{pkg: "github.com/arandu-io/framework/events", name: "NewOutbox"}: {
		display: "events.NewOutbox",
		breaks:  "this code stores domain events in the same transaction as the write that produced them, so that write fails",
	},
	{pkg: "github.com/arandu-io/hesape/events", name: "NewOutbox"}: {
		display: "events.NewOutbox",
		breaks:  "this code stores domain events in the same transaction as the write that produced them, so that write fails",
	},
	// The legacy module remains recognizable for applications that have not yet
	// migrated. Canonical fixtures do not import it and no new rule depends on it.
	{pkg: "github.com/arandu-io/framework/modules/auth", name: "NewService"}: {
		display: "auth.NewService",
		breaks:  "the legacy auth module publishes auth.user.registered inside the transaction that creates the account, so every sign-up fails",
	},
	{pkg: "github.com/arandu-io/framework/modules/auth", name: "New"}: {
		display: "auth.New",
		breaks:  "the legacy auth module publishes auth.user.registered inside the transaction that creates the account, so every sign-up fails",
		rank:    1,
	},
}

// outboxProviders are the registrations that bring the table.
//
// Two, because the relay is registered instead of the plain module when
// something publishes: events.WithRelay carries the same Migrations. A rule that
// knew only NewModule would fire on the more advanced of the two correct
// wirings, which is how a tool teaches people to ignore it.
var outboxProviders = map[importedFunction]bool{
	{pkg: "github.com/arandu-io/framework/events", name: "NewModule"}: true,
	{pkg: "github.com/arandu-io/framework/events", name: "WithRelay"}: true,
}

// importedFunction resolves a package function call through the file's import
// table. It accepts aliases and dot imports, and rejects methods and lookalike
// local values whose qualifier is not an imported package.
func (f *file) importedFunction(called string) (importedFunction, bool) {
	local, name, qualified := strings.Cut(called, ".")
	switch {
	case !qualified:
		local, name = ".", called
	case strings.Contains(name, "."):
		return importedFunction{}, false
	}
	pkg, imported := f.importPath(local)
	if !imported {
		return importedFunction{}, false
	}
	return importedFunction{pkg: pkg, name: name}, true
}

// 16. A module whose writes need another module's table.
//
// The compiler cannot see this and neither can any test the application runs
// against an empty database that it also migrated: auth.Register succeeds in
// development and fails in production the first time somebody signs up, with
// "no such table: outbox", on the screen where a 500 costs the most and where
// the person has no way to route around it. The table travels with
// events.NewModule() -- that is the whole reason the events module exists rather
// than the schema being copied into each project's migrations -- and both
// shipped bootstraps register it. An application that deletes the line while
// tidying gets no warning from anything else.
//
// It is an Error rather than the Warning a new rule normally enters as. That
// policy is about rules that reject code which works, and this one cannot: the
// only project it fires on is one where creating an account already fails a
// hundred percent of the time. Reporting that as a warning would mean CI passes
// on an application whose sign-up is broken.
//
// It concludes from an absence -- no registration anywhere in the project -- so
// it says nothing when a file did not parse, for the reason project.blind
// exists: the missing line may be in the file nobody could read.
//
// # The limit, stated rather than implied
//
// It sees the calls written in this project. A project whose modules are
// registered by a helper living in another repository is one this cannot answer
// about, and it reports the writer as unprovided -- the finding names the line to
// add, so the cost of being wrong there is one line read and dismissed. The
// alternative, resolving registrations across module boundaries, is a type
// checker, and doctor deliberately runs on a project that does not compile.
func theOutboxTableTravelsWithWhatWritesToIt(p *project) []Finding {
	type writer struct {
		file   string
		line   int
		call   string
		breaks string
		rank   int
	}

	var writers []writer
	provided := false

	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.calls(func(call *ast.CallExpr, name string) {
			fn, imported := f.importedFunction(name)
			if !imported {
				return
			}
			if outboxProviders[fn] {
				provided = true
				return
			}
			definition, writes := outboxWriters[fn]
			if !writes {
				return
			}
			file, line := f.at(call)
			writers = append(writers, writer{
				file: file, line: line, call: definition.display,
				breaks: definition.breaks, rank: definition.rank,
			})
		})
	}

	if provided || len(writers) == 0 || len(p.unreadable) > 0 {
		return nil
	}

	// One finding, whatever the project touches the outbox from. The fact is
	// about the project -- there is no outbox table anywhere in it -- and the fix
	// is one line in one file; a report that states it three times, once per call
	// site, is three entries somebody has to read to learn they all say the same
	// thing. The call site chosen is only where the report points, so it is the
	// most useful one: constructions have the lower rank in outboxWriters.
	sort.SliceStable(writers, func(i, j int) bool {
		if a, b := writers[i].rank, writers[j].rank; a != b {
			return a < b
		}
		if writers[i].file != writers[j].file {
			return writers[i].file < writers[j].file
		}
		return writers[i].line < writers[j].line
	})

	w := writers[0]
	return []Finding{{
		Rule: "outbox-not-registered", Severity: Error,
		File: w.file, Line: w.line,
		Message: w.call + " stores domain events and this project registers no outbox table",
		Why: w.breaks + " -- at runtime, with `no such table: outbox`, in front of whoever was using it. " +
			"The table belongs to the events module so that it travels rather than being copied into every project: add events.NewModule() to the k.Register(...) list in bootstrap/app.go, or events.WithRelay(relay) if something already publishes them.",
	}}
}

// sqlStatement is one SQL statement written in a method, as far as doctor can
// read it.
type sqlStatement struct {
	// verb is SELECT, UPDATE or DELETE: the three that carry a predicate.
	verb string
	text string
	node ast.Node
}

// sqlStatementsIn reads the SQL literals of a node, keeping the statements whose
// verb the caller asks for.
//
// Both parameters exist for the same reason: a rule about the WHERE clause wants
// a whole body and only the statements that carry a predicate, and a rule about
// the aggregate boundary wants the inside of one transaction and the INSERT too.
// One reader, asked two different questions.
//
// A query is usually a literal concatenated with a constant holding the column
// list, so the concatenation is flattened and the parts doctor cannot see are
// dropped. That is sound for the question being asked: a column list does not
// contain a WHERE clause, and if the tenant predicate were hidden inside a
// constant this would report a query that is in fact scoped -- which is why the
// message says what it saw.
//
// A statement written entirely in a package-level constant is outside what this
// reads, because the callers pass bodies. Saying so is better than implying
// coverage it does not have.
func sqlStatementsIn(node ast.Node, verb func(string) (string, bool)) []sqlStatement {
	var out []sqlStatement
	record := func(text string, n ast.Node) {
		if v, ok := verb(text); ok {
			out = append(out, sqlStatement{verb: v, text: text, node: n})
		}
	}

	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op != token.ADD {
				return true
			}
			text := concatenatedString(x)
			if text == "" {
				return true
			}
			record(text, x)
			return false
		case *ast.BasicLit:
			if text, ok := stringLiteral(x); ok {
				record(text, x)
			}
		}
		return true
	})
	return out
}

// concatenatedString joins the string literals of a `+` chain, separated by a
// space so that two halves never glue into one token. Anything that is not a
// literal contributes nothing: doctor does not resolve constants.
func concatenatedString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BasicLit:
		if text, ok := stringLiteral(x); ok {
			return text
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return strings.TrimSpace(concatenatedString(x.X) + " " + concatenatedString(x.Y))
		}
	case *ast.ParenExpr:
		return concatenatedString(x.X)
	}
	return ""
}

// scopedVerb reports which of the three statements that carry a predicate this
// text is.
func scopedVerb(text string) (string, bool) {
	return firstVerb(text, "SELECT ", "UPDATE ", "DELETE ")
}

// statementVerb reports which statement this text is, INSERT included.
//
// The aggregate rules need the fourth verb and the tenant rule must not have it:
// the tenant of a row being written is a value in the column list and not a
// predicate, so an INSERT reaching scopedVerb would be reported for a WHERE it
// never had.
func statementVerb(text string) (string, bool) {
	return firstVerb(text, "SELECT ", "INSERT ", "UPDATE ", "DELETE ")
}

// firstVerb returns whichever of the verbs appears earliest in the text.
func firstVerb(text string, verbs ...string) (string, bool) {
	upper := strings.ToUpper(text)
	found, at := "", -1
	for _, verb := range verbs {
		i := strings.Index(upper, verb)
		if i < 0 || (at >= 0 && i >= at) {
			continue
		}
		found, at = strings.TrimSpace(verb), i
	}
	return found, found != ""
}

// tenantIsInThePredicate reports whether the tenant column is in the WHERE.
//
// After the WHERE, not anywhere in the statement: `SELECT id, tenant_id FROM
// invoices WHERE id = ?` names the column and reads every tenant's rows, which
// is the exact statement this rule exists to find.
func tenantIsInThePredicate(text string) bool {
	lower := strings.ToLower(text)
	where := strings.Index(lower, "where")
	if where < 0 {
		return false
	}
	return strings.Contains(lower[where:], "tenant_id")
}

// namesOneRow reports whether a read call is pointed at a particular row.
//
// Find is by key wherever it is written. The model builder's Find and a
// repository's Find both take the id as an argument and answer one entity, so
// the name alone settles it.
//
// Get is the one that needs telling apart, because two different reads are
// spelled that way. A model builder's Get is a listing terminal: it takes the
// context and the Grant, applies the scopes the Grant carries, and answers the
// collection the tenant owns. A repository or service Get written by hand
// answers one entity, and it is handed the key that says which one. So the
// arguments are the tell -- a call given nothing but a context and a Grant has
// nothing in it that could name a row, and a call given more than that is being
// pointed at something.
//
// String constants after the Grant are the exception, because that is where the
// builder's Get takes the columns to select. Selecting columns narrows what
// comes back from each row; it does not narrow the answer to one row.
//
// This reads the arguments as written, never their types, so a key held in a
// constant reads here as a column name. That shape does not arise from a
// request -- an id chosen by the caller arrives as ctx.Param, a field or a
// variable -- and it is the request-supplied id that this rule exists for.
func namesOneRow(call *ast.CallExpr, name string) bool {
	if strings.HasSuffix(name, ".Find") {
		return true
	}
	if !strings.HasSuffix(name, ".Get") || len(call.Args) <= 2 {
		return false
	}
	for _, arg := range call.Args[2:] {
		literal, ok := arg.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
	}
	return false
}

// resourceNotReauthorized checks that the row read from the database is
// re-authorized before being returned.
//
// The first Authorize produces a Grant from a zero value, and the read uses it.
// The second Authorize, with the row that was read, is the object-level
// authorization. A method that skips it compiles and passes all other rules --
// the Grant was received, checked, and the policy exists -- but returns data any
// user of the same tenant may read.
//
// The distinction the rule draws, and it is the whole of it: authorizing an
// action and then fetching one named object is a hole, and authorizing an
// action and then listing the objects the tenant owns is not. A listing was
// already filtered by the Grant it was handed, and there is no second object for
// a second Authorize to be about. See namesOneRow for how the two are told
// apart in the source.
//
// So the rule looks for methods that call Authorize and read one row in the same
// body, and warns when every Authorize comes before the last such read.
func resourceNotReauthorized(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		for _, decl := range f.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			file, line := f.at(fn)

			callsAuthorize := funcBodyContains(fn, func(name string) bool {
				return name == "security.Authorize"
			})
			if !callsAuthorize {
				continue
			}

			lastAuthorize, lastRow := -1, -1
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := callName(call)
				pos := f.fset.Position(call.Pos()).Offset
				if name == "security.Authorize" {
					lastAuthorize = pos
				}
				if namesOneRow(call, name) {
					lastRow = pos
				}
				return true
			})

			if lastRow >= 0 && lastAuthorize < lastRow {
				out = append(out, Finding{
					Rule: "resource-not-reauthorized", Severity: Warning,
					File: file, Line: line,
					Message: fn.Name.Name + " reads a row and does not re-authorize it",
					Why:     "the first Authorize tells whether the caller may look at all. The second, with the row that was read, is the object-level decision: the caller may read this row, and nobody else's. Skipping it means any user of the same tenant sees the row. Call security.Authorize with the entity after the read.",
				})
			}
		}
	}
	return out
}

// 17. What a raw interpolation writes goes to the page as markup.
//
// A view has two interpolation forms and they look almost the same. `{{ }}`
// escapes; `{!! !!}` does not, and is the one place in a view where a string
// becomes HTML. The difference is three characters, and nothing in the source
// says which of the two a given line is entitled to.
//
// A component call is entitled to it. A component is an exported Go function
// that returns template.HTML, and every interpolation inside it was escaped when
// it was generated -- so the markup it returns is markup somebody wrote, not
// markup somebody typed into a form.
//
// An expression that is not a call is a value, and a value here is stored
// cross-site scripting the first time one of them comes from a person. The shape
// is not hypothetical: rendering Markdown produces a string with raw HTML left
// in it, and the line that puts it on the page is `{!! .Body !!}`.
//
// What it cannot see, and it is worth knowing before trusting the report: this
// reads the markup, never the types. A call whose function returns a plain
// string -- Markdown rendered in the view itself -- has the shape of a component
// and passes. A field that already holds template.HTML is a value and does not.
// Separating those two needs the type of the expression, which is why this
// warns rather than fails: it is the shape that is wrong, and the shape is
// evidence, not proof.
func rawOutputIsAComponent(p *project) []Finding {
	var out []Finding
	for _, v := range p.views {
		parsed, err := kyse.Parse(v.rel, v.body)
		if err != nil {
			// A view that does not parse is `aru view:build`'s report, and it
			// makes a better one. Reading the markup of a broken view here would
			// put this rule's name on somebody's syntax error.
			continue
		}
		for _, n := range rawInterpolations(parsed) {
			if isNamedCall(n.Body) {
				continue
			}
			out = append(out, Finding{
				Rule: "raw-output-is-not-a-component", Severity: Warning,
				File: v.rel, Line: n.Line,
				Message: "{!! " + n.Body + " !!} writes a value to the page, not a component",
				Why:     "the raw form escapes nothing, so whatever the expression holds arrives as markup. A component earns that: it is a function returning template.HTML, and what it interpolated was escaped when it was generated. A value has not been through anything -- a bio, a comment or a rendered Markdown body written here is stored cross-site scripting, and it runs for every reader of the page. Write it as {{ " + n.Body + " }}, which escapes, or return it from a component.",
			})
		}
	}
	return out
}

// rawInterpolations collects every `{!! !!}` in a view, wherever it sits.
//
// Sections and the bodies of blocks are walked too: a raw interpolation inside
// @foreach is the one most likely to be a row of somebody's data.
func rawInterpolations(f *kyse.File) []kyse.Node {
	var out []kyse.Node

	var walk func(nodes []kyse.Node)
	walk = func(nodes []kyse.Node) {
		for _, n := range nodes {
			if n.Kind == kyse.Raw {
				out = append(out, n)
			}
			walk(n.Children)
		}
	}

	walk(f.Body)
	for _, s := range f.Sections {
		walk(s.Nodes)
	}
	return out
}

// isNamedCall reports whether the whole expression is a call to a named
// function: a name, optionally qualified by a package, whose argument list opens
// right after it and closes at the end of the expression.
//
// Closing at the END is what makes it the whole expression rather than the start
// of one. `components.Alert(x) + .Body` opens a call and does not end with it,
// and the half that would reach the page unescaped is the half after the plus.
//
// A leading dot is a call too. `.Rich("home.hero.body")` is a method on the page
// data, and the dot is how a view names anything the page carries -- it says
// where the name is looked up, not what kind of thing came back. What separates a
// component from a value here is the argument list, and `.Body` does not have
// one: it stays a value and the rule still reports it.
//
// This read `.Rich(...)` as a value until 31/08/2026, and the cost was on the
// page rather than in a report. A catalogue line holding <code> was written as
// {{ .Rich(...) }} to satisfy the warning, the body escaper escaped what Rich had
// already made safe, and five pages in three languages printed "&lt;code&gt;" to
// their readers. A rule that pushes correct code into a broken shape is worse
// than the rule not existing.
func isNamedCall(expr string) bool {
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, ".")

	i := 0
	for {
		start := i
		for i < len(expr) && isNameByte(expr[i], i > start) {
			i++
		}
		if i == start {
			return false
		}
		if i < len(expr) && expr[i] == '.' {
			i++
			continue
		}
		break
	}
	if i >= len(expr) || expr[i] != '(' {
		return false
	}
	return callClosesAtEnd(expr, i)
}

// isNameByte reports whether a byte may appear in a Go name. A digit is allowed
// everywhere except the first position.
func isNameByte(c byte, inside bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return inside
	}
	return false
}

// callClosesAtEnd reports whether the parenthesis at open is closed by the last
// byte of the expression.
//
// String and rune literals are skipped rather than scanned, because a component
// is very often given a label with a parenthesis in it -- "Close (esc)" -- and
// counting that one would read the call as ending three characters early.
func callClosesAtEnd(expr string, open int) bool {
	depth := 0
	for i := open; i < len(expr); i++ {
		switch c := expr[i]; c {
		case '"', '\'', '`':
			end := endOfLiteral(expr, i)
			if end < 0 {
				return false
			}
			i = end
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(expr)-1
			}
		}
	}
	return false
}

// endOfLiteral returns the index of the byte that closes the literal opening at
// start, or -1 when nothing does.
func endOfLiteral(s string, start int) int {
	quote := s[start]
	for i := start + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			// A raw string has no escapes: the backslash in it is a backslash.
			if quote != '`' {
				i++
			}
		case quote:
			return i
		}
	}
	return -1
}

// retiredModules maps a module whose repository was deleted to the one that
// holds its contents now.
//
// A closed list, and a short one: these four were the adapter repositories, and
// each of them moved into a package of github.com/arandu-io/hesape. The list
// is not a deprecation mechanism and must not grow into one -- what belongs
// here is a module whose repository is gone, which is a fact about the world
// rather than an opinion about style.
//
// github.com/arandu-io/framework/jobs is deliberately absent. It is a bridge
// with a removal date in its own package documentation, it is what
// `aru make:job` writes today, and a check that reports the generator's own
// output is a check people learn to scroll past.
var retiredModules = map[string]string{
	"github.com/arandu-io/database": "github.com/arandu-io/hesape/database",
	"github.com/arandu-io/kv":       "github.com/arandu-io/hesape/redis",
	"github.com/arandu-io/queue":    "github.com/arandu-io/hesape/queue",
	"github.com/arandu-io/storage":  "github.com/arandu-io/hesape/filesystem",
}

// retiredModuleFor reports which retired module an import path belongs to, and
// what answers for it now.
//
// The subpackages travel with the root: github.com/arandu-io/queue/kv is as
// gone as github.com/arandu-io/queue. The prefix has to end at a path element,
// or a project of somebody else's called github.com/arandu-io/queuebase would
// be reported as retired.
//
// Only the module root is named in the answer, not a translated subpath. The
// packages were rearranged on the way in -- the key-value adapter is under a
// different name at the destination -- and a check that guessed the new
// subpath would send people to an import that does not resolve.
func retiredModuleFor(path string) (retired, moved string, ok bool) {
	for module, destination := range retiredModules {
		if path == module || strings.HasPrefix(path, module+"/") {
			return module, destination, true
		}
	}
	return "", "", false
}

// 18. An import of a repository that no longer exists.
//
// Nothing fails when this is written, and that is the whole reason to check it.
// The Go module proxy keeps serving what was published, so the build is green,
// the tests pass and `go mod tidy` resolves -- while the project is pinned to a
// copy of code that has no repository behind it, no fix coming, and a second
// answer to a question the framework already answers.
//
// It is checked by import path and not by symbol, because the import is the
// commitment: a file that names the module has it in go.mod and go.sum whatever
// it does with it afterwards.
//
// Test files are read too. A retired import in a test pins the module exactly
// as hard as one in the application does, and the failure it leads to is the
// same failure a year later.
//
// A warning rather than an error: it reports code that compiles and works
// today, and a check that turns somebody's green pipeline red for a change they
// did not make is one they disable rather than act on.
func noRetiredModuleIsImported(p *project) []Finding {
	var out []Finding
	for _, f := range p.files {
		// The imports are walked in source order rather than through f.imports,
		// so the findings come out in the order the file declares them and each
		// one can point at its own line instead of at the top of the file.
		for _, imp := range f.ast.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			retired, moved, ok := retiredModuleFor(path)
			if !ok {
				continue
			}
			out = append(out, Finding{
				Rule: "retired-module", Severity: Warning,
				File: f.rel, Line: f.line(imp),
				Message: "this file imports " + path + ", and the repository behind it was deleted",
				Why: "what " + retired + " held now lives in " + moved + ", and the two are not kept in step. " +
					"The proxy still serves the old module, so nothing breaks today -- which is why this is worth saying now: " +
					"the project stays on a copy nobody maintains, and every fix and every new driver lands on the other one. " +
					"Import " + moved + " instead.",
			})
		}
	}
	return out
}

// 18b. A test the project cannot run, or scaffolding it ships.
//
// The four checks are the ones the test layout decision asks of every module,
// and they are borrowed from internal/testlayout rather than written here. That
// package answers about two trees: this repository's own suite, which its
// tests/Unit/structure_test.go measures, and the application below. The same
// four statements are true of both, and a second copy of them is how the two
// would come to disagree -- silently, because both copies pass on a tree that
// satisfies them.
//
// The first check is the one worth running a tool for. `go test` runs a file
// only when its name ends in _test.go, so app/Jobs/InvoiceTest.go compiles into
// the package as ordinary code and every test inside it is skipped: no error,
// no warning, a green pipeline over a suite that never executed. The count of
// tests executed is the only place it shows, and nobody reads that.
//
// A warning, not an error. All four report code that compiles and passes today,
// and a check that turns somebody's pipeline red for a layout decision is one
// they switch off rather than act on.
//
// Two things this deliberately does not do.
//
// A check that examined nothing is silent. Every statement here is of the form
// "every X is Y", true of no X at all, and the suite that measures this
// repository fails on it -- because a repository whose tests are not optional
// reading zero test files means the walk broke. An application generated this
// morning has not been given a test yet, and telling its author that a check
// found nothing to check is noise.
//
// A file that does not parse is dropped here rather than reported. It is the
// first rule in this list, as an error, with the syntax error in the message,
// and saying it a second time in weaker words helps nobody.
func testsAreWhereTheyCanRun(p *project) []Finding {
	problems, _, err := testlayout.Run(p.root)
	if err != nil {
		// The walk itself failed -- an unreadable directory, or no go.mod where
		// one has to be. Silent rather than invented: doctor already refuses a
		// directory that is not an Arandu project, so what is left here is a
		// permission problem the shell reports better.
		return nil
	}

	out := make([]Finding, 0, len(problems))
	for _, problem := range problems {
		out = append(out, Finding{
			Rule: problem.Rule, Severity: Warning,
			File: problem.Path, Line: problem.Line,
			Message: problem.Says,
			Why:     problem.Why,
		})
	}
	return out
}

// 19. The performance profile is asked for and the project says it does not run
// there.
//
// The profiles list is what the registry shows and the only thing an installer
// reads before choosing a module, so a module that passes every check below and
// still declares one profile is telling people the opposite of what it does.
//
// A warning, not an error, and the reason is the order the two things happen in:
// running this check is how somebody finds out whether the declaration can be
// made, so failing for the missing declaration would refuse to answer the
// question it was asked. A project with no manifest at all is silent -- an
// application is not published, and demanding the file from every `aru new` is a
// finding on correct code.
func theProfileIsDeclared(p *project) []Finding {
	if p.profile != Performance || p.manifest == nil || len(p.manifest.Profiles) == 0 {
		return nil
	}
	for _, declared := range p.manifest.Profiles {
		if declared == string(Performance) {
			return nil
		}
	}

	name := p.manifest.Name
	if name == "" {
		name = "this project"
	}
	return []Finding{{
		Rule: "profile-not-declared", Severity: Warning,
		File: relativeTo(p.root, p.manifest.Path), Line: 1,
		Message: name + " declares profiles = [" + strings.Join(p.manifest.Profiles, ", ") + "] and is being checked against " + string(Performance),
		Why: "the profiles list is what an installer reads before choosing a module, and it currently says this one does not run here. " +
			"Add \"" + string(Performance) + "\" to it once the rest of this report is clean.",
	}}
}

// 20. A statement that names two tables has no equivalent on the performance
// profile.
//
// A wide-column store keeps one aggregate per partition and has no join, so the
// query has to become one read per entity with the result assembled in Go. That
// is a different repository, not a different dialect, which is why it is worth
// knowing before the module declares it supports the profile rather than after.
//
// It counts TABLES rather than looking for the word JOIN, and the difference is
// what keeps it off correct code: the List the generator emits carries a keyset
// cursor written as a subquery over its own table, which is one aggregate and
// runs on both profiles. Counting tables reads that as one and reads
// `FROM invoices, lines` -- a join with no JOIN in it -- as two.
//
// # What it does not see
//
// The same blind spots sqlStatements has, and they are the honest limit of the
// answer: a statement held entirely in a package-level constant, and one
// assembled from a variable. Both are invisible here, so a clean report on the
// performance profile means no join was found, not that none exists.
func queriesReachOneAggregate(p *project) []Finding {
	if p.profile != Performance {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		f.functions(func(fn *ast.FuncDecl) {
			if fn.Body == nil {
				return
			}
			for _, sql := range sqlStatementsIn(fn.Body, statementVerb) {
				tables := tablesNamed(sql.text)
				if len(tables) < 2 {
					continue
				}
				file, line := f.at(sql.node)
				out = append(out, Finding{
					Rule: "join-across-aggregates", Severity: Error,
					File: file, Line: line,
					Message: "the " + sql.verb + " in " + fn.Name.Name + " reads " + strings.Join(tables, " and "),
					Why: "the performance profile stores one aggregate per partition and has no join, so this statement has no equivalent there: it becomes one query per entity, joined in Go. " +
						"On the conventional profile it is correct, which is why it is reported only when the performance profile is asked for.",
				})
			}
		})
	}
	return out
}

// 21. A transaction that spans two aggregates has no equivalent either.
//
// A wide-column store has no transaction across partitions, so a write that
// needs two of them to succeed or fail together cannot be expressed: it becomes
// two writes and something that reconciles them, which is a design decision and
// not a port.
//
// # The two shapes it reads, and the one it does not
//
// data.Transaction with a function literal is the framework's transaction, and
// the region is the literal's body. A raw Begin or BeginTx on the handle is the
// other, and there the region runs from the call to the end of the enclosing
// function, because nothing marks where such a transaction ends.
//
// Inside a region it counts aggregates two ways and never mixes them: the tables
// of the SQL written there, or -- when there is none -- the repository fields the
// region calls, which is the shape a service has, with the SQL one level down in
// the repositories. Mixing them would count `invoices` and InvoiceRepository as
// two aggregates when they are one.
//
// A transaction whose repositories arrive as locals rather than fields, or whose
// work sits in a function called from inside it, is not seen. Under-reporting is
// the direction to be wrong in: a rule that invents a cross-aggregate
// transaction sends somebody to redesign a write that was already fine.
func transactionsStayInsideOneAggregate(p *project) []Finding {
	if p.profile != Performance {
		return nil
	}

	var out []Finding
	for _, f := range p.files {
		if f.isTest {
			continue
		}
		fields := repositoryFields(f)
		f.functions(func(fn *ast.FuncDecl) {
			for _, region := range transactionRegions(fn) {
				touched, kind := aggregatesTouched(region, fields)
				if len(touched) < 2 {
					continue
				}
				file, line := f.at(region.opened)
				out = append(out, Finding{
					Rule: "transaction-across-aggregates", Severity: Error,
					File: file, Line: line,
					Message: "the transaction in " + fn.Name.Name + " writes " + kind + " " + strings.Join(touched, " and "),
					Why: "the performance profile has no transaction across partitions, so these writes cannot commit or roll back together there: one succeeds and the other does not, and something has to reconcile them. " +
						"On the conventional profile it is correct, which is why it is reported only when the performance profile is asked for.",
				})
			}
		})
	}
	return out
}

// transactionRegion is the part of a body that runs inside a transaction.
type transactionRegion struct {
	// opened is the call that started it, which is where the finding points.
	opened ast.Node
	// node is what to read for the work: the literal's body, or the whole
	// enclosing body when the transaction has no literal to bound it.
	node ast.Node
	// from and to bound the region inside node.
	from, to token.Pos
}

// transactionRegions finds the transactions a function opens.
func transactionRegions(fn *ast.FuncDecl) []transactionRegion {
	if fn.Body == nil {
		return nil
	}

	var out []transactionRegion
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := callName(call); {
		case name == "data.Transaction":
			// The work is the literal, whatever position it was passed in.
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.FuncLit)
				if !ok || lit.Body == nil {
					continue
				}
				out = append(out, transactionRegion{
					opened: call, node: lit.Body,
					from: lit.Body.Pos(), to: lit.Body.End(),
				})
			}
		case strings.HasSuffix(name, ".Begin"), strings.HasSuffix(name, ".BeginTx"):
			out = append(out, transactionRegion{
				opened: call, node: fn.Body,
				from: call.Pos(), to: fn.Body.End(),
			})
		}
		return true
	})
	return out
}

// aggregatesTouched names what the region writes, and says which of the two
// signals answered.
//
// The SQL comes first because it names the table, which is what the person
// reading the finding has to go and look at. The repository fields answer for the
// service that opens the transaction and calls down, where there is no SQL to
// read at this level at all.
func aggregatesTouched(region transactionRegion, fields map[string]string) ([]string, string) {
	var tables []string
	seen := map[string]bool{}
	for _, sql := range sqlStatementsIn(region.node, statementVerb) {
		if sql.node.Pos() < region.from || sql.node.Pos() >= region.to {
			continue
		}
		for _, table := range tablesNamed(sql.text) {
			if !seen[table] {
				seen[table] = true
				tables = append(tables, table)
			}
		}
	}
	if len(tables) > 0 {
		return tables, "the tables"
	}

	var repositories []string
	called := map[string]bool{}
	ast.Inspect(region.node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() < region.from || call.Pos() >= region.to {
			return true
		}
		// s.invoices.Create: the field is the segment before the method.
		parts := strings.Split(callName(call), ".")
		if len(parts) < 2 {
			return true
		}
		repository, known := fields[parts[len(parts)-2]]
		if !known || called[repository] {
			return true
		}
		called[repository] = true
		repositories = append(repositories, repository)
		return true
	})
	return repositories, "through"
}

// repositoryFields maps a field name to the repository type it holds, for every
// struct the file declares.
//
// The file, not the project: doctor never resolves types across files, and the
// struct that holds the repositories is declared next to the method that uses
// them in every tree the generator emits.
func repositoryFields(f *file) map[string]string {
	out := map[string]string{}
	f.types(func(ts *ast.TypeSpec) {
		st, ok := ts.Type.(*ast.StructType)
		if !ok || st.Fields == nil {
			return
		}
		for _, field := range st.Fields.List {
			name := exprName(field.Type)
			if i := strings.LastIndex(name, "."); i >= 0 {
				name = name[i+1:]
			}
			if !looksLikeRepository(name) {
				continue
			}
			for _, ident := range field.Names {
				out[ident.Name] = name
			}
		}
	})
	return out
}

// tablesNamed returns the tables a statement reads or writes, lowercased, in the
// order they appear and without repeats.
//
// It reads the four words a table name can follow -- FROM, JOIN, INTO and UPDATE
// -- and the comma list after FROM, which is a join written without the word. A
// name qualified by a schema is reduced to the table, so `public.invoices` and
// `invoices` are one table and not two.
//
// A keyword where a name was expected ends the list rather than being taken for
// a table: `FROM (SELECT ...` names nothing here, and the subquery's own FROM is
// read on its own.
func tablesNamed(text string) []string {
	tokens := sqlTokens(text)

	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	name := func(i int) (string, bool) {
		if i >= len(tokens) {
			return "", false
		}
		if t := tokens[i]; t == "," || t == "(" || t == ")" || sqlKeywords[t] {
			return "", false
		}
		return tokens[i], true
	}

	for i := 0; i < len(tokens); i++ {
		switch tokens[i] {
		case "from", "join", "into", "update":
		default:
			continue
		}
		for {
			table, ok := name(i + 1)
			if !ok {
				break
			}
			add(table)
			i++
			// An alias sits between the table and the comma, and is not a table.
			if _, alias := name(i + 1); alias {
				i++
			}
			if i+1 < len(tokens) && tokens[i+1] == "," {
				i++
				continue
			}
			break
		}
	}
	return out
}

// sqlTokens splits a statement into names and the three characters that carry
// structure. A dot stays inside a name, so a schema-qualified table arrives whole.
func sqlTokens(text string) []string {
	var out []string
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, strings.ToLower(word.String()))
			word.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			word.WriteRune(r)
		default:
			flush()
			if r == ',' || r == '(' || r == ')' {
				out = append(out, string(r))
			}
		}
	}
	flush()
	return out
}

// sqlKeywords is what may not be mistaken for a table name.
//
// It only has to hold the words that can follow FROM, JOIN, INTO or UPDATE, which
// is why it is this short: everything else in a statement is never read as a name.
var sqlKeywords = map[string]bool{
	"all": true, "and": true, "as": true, "asc": true, "by": true, "cross": true,
	"delete": true, "desc": true, "distinct": true, "except": true, "for": true,
	"from": true, "full": true, "group": true, "having": true, "inner": true,
	"insert": true, "intersect": true, "into": true, "join": true, "lateral": true,
	"left": true, "limit": true, "natural": true, "offset": true, "on": true,
	"or": true, "order": true, "outer": true, "returning": true, "right": true,
	"select": true, "set": true, "union": true, "update": true, "using": true,
	"values": true, "where": true, "window": true, "with": true,
}

// hesapeMigrations is the package a migration is written against.
const hesapeMigrations = "github.com/arandu-io/hesape/database/migrations"

// migrationDecl is one migration the project declares, with the two methods the
// rules below reason about.
type migrationDecl struct {
	f    *file
	name string
	line int
	up   *ast.FuncDecl
	down *ast.FuncDecl
}

// migrationDecls finds every migration in the project.
//
// A migration is a type that embeds migrations.BaseMigration, not a file in a
// directory that happens to be called migrations. The embed is the contract and
// the directory is only the convention: a project that keeps them somewhere else
// still has them found, and a helper sitting beside them is not counted as one.
//
// Methods are collected across the whole package rather than from the file that
// declares the type, because nothing requires Up and Down to be written next to
// it -- and a rule that concluded "no Down" from one file would report the
// project that split them.
func (p *project) migrationDecls() []migrationDecl {
	type key struct{ dir, typ string }

	found := map[key]migrationDecl{}
	methods := map[key]map[string]*ast.FuncDecl{}

	for _, f := range p.files {
		if f.isTest {
			continue
		}

		if local, imported := f.imports[hesapeMigrations]; imported && local != "_" && local != "." {
			f.types(func(ts *ast.TypeSpec) {
				st, isStruct := ts.Type.(*ast.StructType)
				if !isStruct {
					return
				}
				for _, field := range st.Fields.List {
					// An embedded field has no name, and the type is what says
					// which one it is.
					if len(field.Names) > 0 {
						continue
					}
					if exprName(field.Type) != local+".BaseMigration" {
						continue
					}
					found[key{f.dir, ts.Name.Name}] = migrationDecl{f: f, name: ts.Name.Name, line: f.line(ts)}
				}
			})
		}

		f.functions(func(fn *ast.FuncDecl) {
			recv := receiverType(fn)
			if recv == "" {
				return
			}
			k := key{f.dir, recv}
			if methods[k] == nil {
				methods[k] = map[string]*ast.FuncDecl{}
			}
			methods[k][fn.Name.Name] = fn
		})
	}

	out := make([]migrationDecl, 0, len(found))
	for k, decl := range found {
		decl.up = methods[k]["Up"]
		decl.down = methods[k]["Down"]
		out = append(out, decl)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].f.rel != out[j].f.rel {
			return out[i].f.rel < out[j].f.rel
		}
		return out[i].line < out[j].line
	})
	return out
}

// migrationsMustReachTheBinary: a migration nobody imports is a file that does
// nothing and says nothing.
//
// This is the one thing in the whole migration path that fails by succeeding.
// Go leaves a package nobody imports out of the binary, so the init that
// registers the migration never runs, the registry is empty, and `aru migrate`
// reports that there is nothing to apply -- which is exactly what it prints on a
// database that is already up to date. Nothing anywhere says the difference.
//
// `aru make:migration` prints the import to add, and that print happens once, at
// the moment the file is written, on a terminal somebody has already scrolled
// past. This is the standing check: the same fact, asked again every time doctor
// runs, including on the project that arrived by clone.
//
// It concludes from an absence, so it says nothing when it cannot see: no module
// path means the import cannot be recognised, and a file that did not parse
// might be the one holding it.
func migrationsMustReachTheBinary(p *project) []Finding {
	declared := p.migrationDecls()
	if len(declared) == 0 || p.modulePath == "" || len(p.unreadable) > 0 {
		return nil
	}

	// Where the migrations live, and the first one in each place, which is where
	// the report points.
	first := map[string]migrationDecl{}
	count := map[string]int{}
	for _, d := range declared {
		if _, seen := first[d.f.dir]; !seen {
			first[d.f.dir] = d
		}
		count[d.f.dir]++
	}

	dirs := make([]string, 0, len(first))
	for dir := range first {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	var out []Finding
	for _, dir := range dirs {
		pkg := path.Join(p.modulePath, dir)

		linked := false
		for _, f := range p.files {
			// The package importing itself is not the import that links it: the
			// question is whether something ELSE in the binary names it.
			if f.dir == dir {
				continue
			}
			if _, imports := f.imports[pkg]; imports {
				linked = true
				break
			}
		}
		if linked {
			continue
		}

		d := first[dir]
		out = append(out, Finding{
			Rule: "migrations-not-linked", Severity: Warning,
			File: d.f.rel, Line: d.line,
			Message: fmt.Sprintf("nothing in this project imports %s, so the init that registers %s never runs", pkg, d.name),
			Why: fmt.Sprintf("Go leaves a package nobody imports out of the binary, so all %d migration(s) here are absent from it and the registry `aru migrate` reads is empty. "+
				"It does not fail: it reports that there is nothing to apply, which is the same thing it says about a database that is already migrated, and the schema is never created. "+
				"Blank-import the package where the application is wired -- `_ %q` in bootstrap/app.go.", count[dir], pkg),
		})
	}
	return out
}

// addedColumnsMustBeNullable: a NOT NULL column added to a table that has rows
// fails on every row already in it.
//
// It is the rollout that breaks rather than the migration. During one, the
// previous binary is still inserting rows and knows nothing about the new
// column, so a NOT NULL without a default fails on its first insert -- and the
// migration itself fails on every row already there. The column is added
// nullable, backfilled, and tightened in a later migration.
//
// Only the alter path is read. A table being created has no rows and no older
// binary writing to it, so NOT NULL is correct there and a rule that fired on it
// would fire on almost every first migration ever written.
//
// `aru make:migration --table` already refuses a column declared required, and
// this is not a second copy of that: the generator can only refuse what it is
// about to write, and every migration that was hand-written, or edited after it
// was generated, reaches a database without ever passing that check.
func addedColumnsMustBeNullable(p *project) []Finding {
	var out []Finding
	for _, d := range p.migrationDecls() {
		if d.up == nil {
			continue
		}
		for _, block := range blueprintBlocks(d.up, "Table") {
			for _, col := range block.columns() {
				if col.nullable || col.changed {
					continue
				}
				out = append(out, Finding{
					Rule: "added-column-not-nullable", Severity: Warning,
					File: d.f.rel, Line: d.f.line(col.at),
					Message: fmt.Sprintf("%s is added to the existing table %s without Nullable() or a default", col.name, block.table),
					Why: "A NOT NULL column added to a table that has rows fails on every row already there, so the migration does not apply at all. " +
						"During a rollout it fails a second way: the previous binary is still inserting rows and does not know this column exists, so its next insert is rejected while it is still serving. " +
						"Add it with .Nullable(), backfill the rows, and tighten it in a later migration.",
				})
			}
		}
	}
	return out
}

// migrationsMustBeReversible: a migration that reverses nothing stops the
// rollback of everything applied beside it.
//
// The migrator refuses it rather than pretending. A migration that declares
// neither a Down nor Irreversible is named and the rollback stops there: the
// record is kept, the schema is untouched, and nothing is reported as undone
// that was not. One that declares Irreversible is left applied on purpose, with
// its record and the reason printed beside its name, and the batch carries on
// past it.
//
// So the failure this warns about is no longer a silent one, and that is exactly
// why the warning is still worth having. Refusing is the right thing to do and
// it is still a stop: the rollback halts at the first migration nobody finished,
// and every migration applied beside it in the same batch stays applied because
// the rollback never reaches them. That is discovered by running
// `aru migrate:rollback`, which is a command people reach for when something is
// already wrong. This is discovered while the migration is being written.
//
// It only reports the migrations for which a Down could actually be written: one
// that created a table, added a column or created an index. A backfill is left
// alone, because `UPDATE ... WHERE total IS NULL` cannot be reversed by anything
// -- the rows it changed are no longer distinguishable from the rows it did not
// -- and telling somebody to write a Down they cannot write is how a report
// teaches people to stop reading it. That backfill is what Irreversible exists
// for, and declaring it is what lets a rollback carry on past it instead of
// stopping on it.
func migrationsMustBeReversible(p *project) []Finding {
	var out []Finding
	for _, d := range p.migrationDecls() {
		if d.up == nil || d.down != nil {
			continue
		}
		what := reversibleChange(d.f, d.up)
		if what == "" {
			continue
		}
		out = append(out, Finding{
			Rule: "rollback-does-nothing", Severity: Warning,
			File: d.f.rel, Line: d.f.line(d.up),
			Message: fmt.Sprintf("%s %s and declares no Down", d.name, what),
			Why: "A migration that declares neither a Down nor Irreversible cannot be rolled back, and `aru migrate:rollback` refuses it by name rather than reporting a change it did not make: the record is kept and the schema is left as it is. " +
				"The rollback stops there, so every migration applied beside this one in the same batch stays applied too -- and that is found out by running the one command whose whole purpose is to undo, usually while something is already wrong. " +
				"Write the Down that undoes this change, or declare Irreversible with the reason nothing can.",
		})
	}
	return out
}

// blueprintBlock is one Schema().Create or Schema().Table call: the table it
// names and the closure it hands the Blueprint to.
type blueprintBlock struct {
	table string
	param string
	body  *ast.BlockStmt
}

// blueprintBlocks finds the Blueprint closures a method opens, by which builder
// method opened them -- "Create" for a new table, "Table" for an alter.
//
// The receiver is matched by suffix rather than by name because nothing fixes
// what the connection is called: conn.Schema().Table and c.Schema().Table are
// the same call, and a rule keyed to one spelling silently stops applying to the
// other.
func blueprintBlocks(fn *ast.FuncDecl, method string) []blueprintBlock {
	if fn.Body == nil {
		return nil
	}

	var out []blueprintBlock
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall || !strings.HasSuffix(callName(call), ".Schema()."+method) || len(call.Args) < 2 {
			return true
		}
		lit, isLit := call.Args[len(call.Args)-1].(*ast.FuncLit)
		if !isLit || len(lit.Type.Params.List) == 0 || len(lit.Type.Params.List[0].Names) == 0 {
			return true
		}
		table, _ := stringLiteral(call.Args[1])
		out = append(out, blueprintBlock{
			table: table,
			param: lit.Type.Params.List[0].Names[0].Name,
			body:  lit.Body,
		})
		return true
	})
	return out
}

// columnAdd is one column a Blueprint closure declares.
type columnAdd struct {
	name     string
	at       ast.Node
	nullable bool
	// changed marks a redefinition of a column that is already there. It is not
	// an addition, and what makes it safe or not is what the column held before,
	// which is not in this file.
	changed bool
}

// columns reads the columns a Blueprint closure declares.
//
// What counts as a column is decided by exclusion, and that direction is the
// whole reliability of this: the column types are a grammar that grows with
// every engine feature anybody wants, while the commands that are not columns --
// the drops, the renames and the indexes -- are a closed set that has not moved.
// A list of column types would be one release behind from the day it was
// written, and being behind means silently passing the column it had not heard
// of.
func (b blueprintBlock) columns() []columnAdd {
	var out []columnAdd
	for _, stmt := range b.body.List {
		expr, isExpr := stmt.(*ast.ExprStmt)
		if !isExpr {
			continue
		}
		chain := exprName(expr.X)
		if !strings.HasPrefix(chain, b.param+".") {
			continue
		}

		// The innermost call on the Blueprint is the one that declares the
		// column; everything to its right is a modifier.
		var base *ast.CallExpr
		ast.Inspect(expr.X, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			if id, isID := sel.X.(*ast.Ident); isID && id.Name == b.param {
				base = call
			}
			return true
		})
		if base == nil || len(base.Args) == 0 {
			continue
		}
		method := base.Fun.(*ast.SelectorExpr).Sel.Name
		if strings.HasPrefix(method, "Drop") || strings.HasPrefix(method, "Rename") || blueprintNotAColumn[method] {
			continue
		}
		// A column is named by a string. The index commands that take one column
		// take a slice, so this is also what tells the two apart.
		name, named := stringLiteral(base.Args[0])
		if !named {
			continue
		}

		out = append(out, columnAdd{
			name: name,
			at:   base,
			nullable: strings.Contains(chain, ".Nullable()") ||
				strings.Contains(chain, ".Default()") ||
				strings.Contains(chain, ".UseCurrent()"),
			changed: strings.Contains(chain, ".Change()"),
		})
	}
	return out
}

// blueprintNotAColumn is what a Blueprint does that is not adding a column,
// minus everything spelled Drop... or Rename..., which is matched by prefix.
//
// It is a closed list for the reason appCategories is one: these are the
// commands, and the commands do not grow the way the column types do.
var blueprintNotAColumn = map[string]bool{
	"Create": true, "Engine": true, "InnoDb": true, "Charset": true,
	"Collation": true, "Temporary": true, "Comment": true,
	"Primary": true, "Unique": true, "Index": true, "FullText": true,
	"SpatialIndex": true, "VectorIndex": true, "RawIndex": true, "Foreign": true,
}

// reversibleChange says what an Up did that a Down could undo, or empty.
//
// Empty is the answer for a backfill, and that is the point of the function:
// what separates a migration somebody forgot to reverse from one that cannot be
// reversed is whether the change is in the schema or in the rows.
func reversibleChange(f *file, fn *ast.FuncDecl) string {
	if len(blueprintBlocks(fn, "Create")) > 0 {
		return "creates a table"
	}
	for _, block := range blueprintBlocks(fn, "Table") {
		if len(block.columns()) > 0 {
			return "adds a column"
		}
	}

	// The escape hatch is still a schema change: a migration that writes its own
	// DDL is the one most likely to have been written by hand, which is where a
	// missing Down comes from in the first place.
	found := ""
	f.functionBodies(func(body *ast.BlockStmt) {
		if body != fn.Body || found != "" {
			return
		}
		ast.Inspect(body, func(n ast.Node) bool {
			lit, isLit := n.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				return true
			}
			text := strings.Join(strings.Fields(strings.ToUpper(lit.Value)), " ")
			switch {
			case strings.Contains(text, "CREATE TABLE"):
				found = "creates a table"
			case strings.Contains(text, "ADD COLUMN"):
				found = "adds a column"
			case strings.Contains(text, "CREATE INDEX"), strings.Contains(text, "CREATE UNIQUE INDEX"):
				found = "creates an index"
			default:
				return true
			}
			return false
		})
	})
	return found
}
