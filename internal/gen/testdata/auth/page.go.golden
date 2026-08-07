package views

// Page is the part of every page the layout draws.
//
// A page struct embeds it and satisfies Layout for free:
//
//	type InvoicesIndexData struct {
//	    Page                 // the chrome: brand, session, CSRF, navigation
//	    Invoices []Invoice   // what only this page has
//	}
//
// One struct per page still exists, and that is what makes a typo a compile
// error. What it stops repeating is the state the layout needs.
//
// The fields are exported and the methods are what the layout reads. The
// indirection buys the thing that was missing: a layout typed by an interface
// renders any page that fits it, and a layout typed by one page's struct
// renders that page and answers 500 for every other -- with a green build,
// because the disagreement is a type assertion at run time.
type Page struct {
	// Title is the document title, already including the application name.
	Title string
	// AppName is the brand in the navigation bar.
	AppName string

	// Token is the CSRF token issued for this session. It reaches the markup
	// twice: as the hidden field @csrf writes, and as the hx-headers attribute
	// on <body> that makes every HTMX request carry it.
	Token string

	// Authenticated decides which half of the navigation is drawn.
	Authenticated bool
	// UserName is the signed-in person's display name.
	UserName string
	// HasRegister draws the sign-up link only where something answers it.
	HasRegister bool

	// The navigation targets. Fields rather than a route() helper the view
	// reaches for on its own: a name that drifts is then a compile error
	// instead of a blank link.
	HomeURL     string
	LoginURL    string
	LogoutURL   string
	RegisterURL string
}

// Compile-time proof that this satisfies what the layout renders with. If the
// layout asks for something new, the build stops here, in one file, naming the
// method -- instead of at the type assertion of whichever page rendered first.
var _ Layout = Page{}

// PageTitle is the document title.
func (p Page) PageTitle() string { return p.Title }

// BrandName is the application name in the navigation bar.
func (p Page) BrandName() string { return p.AppName }

// CSRFToken is the token for this session.
func (p Page) CSRFToken() string { return p.Token }

// SignedIn reports whether there is a session.
func (p Page) SignedIn() bool { return p.Authenticated }

// SignedInName is the display name of the signed-in person.
func (p Page) SignedInName() string { return p.UserName }

// ShowRegister reports whether the sign-up link is drawn.
func (p Page) ShowRegister() bool { return p.HasRegister }

// HomeLink is the landing page.
func (p Page) HomeLink() string { return p.HomeURL }

// LoginLink is the sign-in screen.
func (p Page) LoginLink() string { return p.LoginURL }

// LogoutLink is where the sign-out form posts.
func (p Page) LogoutLink() string { return p.LogoutURL }

// RegisterLink is the sign-up screen.
func (p Page) RegisterLink() string { return p.RegisterURL }
