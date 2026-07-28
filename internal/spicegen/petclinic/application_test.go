package spicegen

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/config"
	"github.com/StevenBuglione/spice/lifecycle"
	"github.com/StevenBuglione/spice/web"
)

func TestGeneratedPetclinicServesWelcomeAndManagement(t *testing.T) {
	t.Parallel()

	application, err := NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(t.Context()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	handler := application.Handler()
	if handler == nil {
		t.Fatal("generated application handler is nil")
	}

	welcome := httptest.NewRecorder()
	handler.ServeHTTP(
		welcome,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if welcome.Code != http.StatusOK ||
		!strings.Contains(welcome.Body.String(), "<h1>Welcome</h1>") {
		t.Fatalf("welcome response = %d %s", welcome.Code, welcome.Body)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(
		health,
		httptest.NewRequest(http.MethodGet, "/actuator/health", nil),
	)
	if health.Code != http.StatusOK ||
		!strings.Contains(health.Body.String(), `"status":"UP"`) {
		t.Fatalf("health response = %d %s", health.Code, health.Body)
	}
}

func TestGeneratedPetclinicOwnerWorkflow(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{Logger: testLogger()},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(t.Context()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	handler := application.Handler()
	if handler == nil {
		t.Fatal("generated application handler is nil")
	}

	find := servePetclinic(handler, http.MethodGet, "/owners/find", nil)
	if find.Code != http.StatusOK ||
		!strings.Contains(find.Body.String(), "<h1>Find Owners</h1>") {
		t.Fatalf("find form = %d %s", find.Code, find.Body)
	}
	multiple := servePetclinic(
		handler,
		http.MethodGet,
		"/owners?lastName=Dav&page=1",
		nil,
	)
	if multiple.Code != http.StatusOK ||
		!strings.Contains(multiple.Body.String(), "Betty Davis") ||
		!strings.Contains(multiple.Body.String(), "Harold Davis") {
		t.Fatalf("owner list = %d %s", multiple.Code, multiple.Body)
	}
	single := servePetclinic(
		handler,
		http.MethodGet,
		"/owners?lastName=Franklin",
		nil,
	)
	if single.Code != http.StatusSeeOther ||
		single.Header().Get("Location") != "/owners/1" {
		t.Fatalf("single owner = %d %#v", single.Code, single.Header())
	}
	details := servePetclinic(handler, http.MethodGet, "/owners/1", nil)
	if details.Code != http.StatusOK ||
		!strings.Contains(details.Body.String(), "George Franklin") ||
		!strings.Contains(details.Body.String(), "Leo") {
		t.Fatalf("owner details = %d %s", details.Code, details.Body)
	}

	invalid := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/new",
		url.Values{
			"firstName": {""},
			"lastName":  {"Owner"},
			"address":   {"One Main Street"},
			"city":      {"Madison"},
			"telephone": {"invalid"},
		},
	)
	if invalid.Code != http.StatusOK ||
		!strings.Contains(invalid.Body.String(), "firstName") ||
		!strings.Contains(invalid.Body.String(), "telephone") {
		t.Fatalf("invalid owner = %d %s", invalid.Code, invalid.Body)
	}

	created := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/new",
		validOwnerForm("Ada", "Lovelace"),
	)
	if created.Code != http.StatusSeeOther ||
		created.Header().Get("Location") != "/owners/11" {
		t.Fatalf("created owner = %d %#v", created.Code, created.Header())
	}
	createdDetails := servePetclinic(
		handler,
		http.MethodGet,
		"/owners/11",
		nil,
	)
	if createdDetails.Code != http.StatusOK ||
		!strings.Contains(createdDetails.Body.String(), "Ada Lovelace") {
		t.Fatalf(
			"created owner details = %d %s",
			createdDetails.Code,
			createdDetails.Body,
		)
	}

	updated := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/1/edit",
		validOwnerForm("Georgina", "Franklin"),
	)
	if updated.Code != http.StatusSeeOther ||
		updated.Header().Get("Location") != "/owners/1" {
		t.Fatalf("updated owner = %d %#v", updated.Code, updated.Header())
	}
	updatedDetails := servePetclinic(
		handler,
		http.MethodGet,
		"/owners/1",
		nil,
	)
	if updatedDetails.Code != http.StatusOK ||
		!strings.Contains(updatedDetails.Body.String(), "Georgina Franklin") ||
		!strings.Contains(updatedDetails.Body.String(), "Leo") {
		t.Fatalf(
			"updated owner details = %d %s",
			updatedDetails.Code,
			updatedDetails.Body,
		)
	}
}

func TestGeneratedPetclinicOwnerRouteBoundaries(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{Logger: testLogger()},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(t.Context()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	handler := application.Handler()
	if handler == nil {
		t.Fatal("generated application handler is nil")
	}

	for _, target := range []string{
		"/",
		"/owners",
		"/owners/find",
		"/owners/new",
		"/owners/1",
		"/owners/1/edit",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotAcceptable {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusNotAcceptable)
		}
	}
	for _, target := range []string{"/owners/new", "/owners/1/edit"} {
		request := httptest.NewRequest(
			http.MethodPost,
			target,
			strings.NewReader(validOwnerForm("Ada", "Lovelace").Encode()),
		)
		request.Header.Set(
			"Content-Type",
			"application/x-www-form-urlencoded",
		)
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotAcceptable {
			t.Errorf("%s POST status = %d, want %d", target, response.Code, http.StatusNotAcceptable)
		}
	}
	for _, target := range []string{
		"/owners?page=not-a-number",
		"/owners?lastName=one&lastName=two",
		"/owners/not-a-number",
		"/owners/not-a-number/edit",
	} {
		response := servePetclinic(handler, http.MethodGet, target, nil)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusBadRequest)
		}
	}
	missing := servePetclinic(handler, http.MethodGet, "/owners/999", nil)
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing owner status = %d, want %d", missing.Code, http.StatusNotFound)
	}
	for _, target := range []string{"/owners/new", "/owners/1/edit"} {
		response := servePetclinic(handler, http.MethodGet, target, nil)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", target, response.Code, http.StatusOK)
		}
	}

	missingForm := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/new",
		url.Values{},
	)
	if missingForm.Code != http.StatusOK ||
		!strings.Contains(missingForm.Body.String(), "firstName") {
		t.Errorf("missing form = %d %s", missingForm.Code, missingForm.Body)
	}
	unknownForm := validOwnerForm("Ada", "Lovelace")
	unknownForm.Set("unexpected", "value")
	unknown := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/new",
		unknownForm,
	)
	if unknown.Code != http.StatusOK ||
		!strings.Contains(unknown.Body.String(), "unexpected") ||
		!strings.Contains(unknown.Body.String(), "is not allowed") {
		t.Errorf("unknown form = %d %s", unknown.Code, unknown.Body)
	}
	for _, field := range []string{
		"firstName",
		"lastName",
		"address",
		"city",
		"telephone",
	} {
		repeatedForm := validOwnerForm("Ada", "Lovelace")
		repeatedForm.Add(field, "duplicate")
		repeated := servePetclinic(
			handler,
			http.MethodPost,
			"/owners/new",
			repeatedForm,
		)
		if repeated.Code != http.StatusOK ||
			!strings.Contains(repeated.Body.String(), field) {
			t.Errorf("repeated %s = %d %s", field, repeated.Code, repeated.Body)
		}
	}
	malformedRequest := httptest.NewRequest(
		http.MethodPost,
		"/owners/new",
		strings.NewReader("firstName=Ada"),
	)
	malformed := httptest.NewRecorder()
	handler.ServeHTTP(malformed, malformedRequest)
	if malformed.Code != http.StatusOK ||
		!strings.Contains(malformed.Body.String(), "Content-Type") {
		t.Errorf("malformed form = %d %s", malformed.Code, malformed.Body)
	}
	badEditPath := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/not-a-number/edit",
		validOwnerForm("George", "Franklin"),
	)
	if badEditPath.Code != http.StatusBadRequest {
		t.Errorf("bad edit path status = %d", badEditPath.Code)
	}
	missingEdit := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/999/edit",
		validOwnerForm("Missing", "Owner"),
	)
	if missingEdit.Code != http.StatusNotFound {
		t.Errorf("missing edit status = %d", missingEdit.Code)
	}
	invalidEdit := servePetclinic(
		handler,
		http.MethodPost,
		"/owners/1/edit",
		url.Values{},
	)
	if invalidEdit.Code != http.StatusOK ||
		!strings.Contains(invalidEdit.Body.String(), "firstName") {
		t.Errorf("invalid edit = %d %s", invalidEdit.Code, invalidEdit.Body)
	}
}

func TestGeneratedPetclinicResponseWriteFailures(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{Logger: testLogger()},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(t.Context()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	handler := application.Handler()
	if handler == nil {
		t.Fatal("generated application handler is nil")
	}

	for _, target := range []string{
		"/",
		"/owners",
		"/owners/find",
		"/owners/new",
		"/owners/1",
		"/owners/1/edit",
	} {
		handler.ServeHTTP(
			&errorResponseWriter{header: make(http.Header)},
			httptest.NewRequest(http.MethodGet, target, nil),
		)
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Accept", "application/json")
		handler.ServeHTTP(
			&errorResponseWriter{header: make(http.Header)},
			request,
		)
	}
	for _, target := range []string{
		"/owners?page=invalid",
		"/owners/not-a-number",
		"/owners/not-a-number/edit",
	} {
		handler.ServeHTTP(
			&errorResponseWriter{header: make(http.Header)},
			httptest.NewRequest(http.MethodGet, target, nil),
		)
	}
}

func TestGeneratedPetclinicLifecycleAndTypedComponents(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{Logger: testLogger()},
	)
	if err != nil {
		t.Fatal(err)
	}
	components := application.Components()
	if components.PetclinicDatabase == nil ||
		components.OwnerRepository == nil ||
		components.PetTypeRepository == nil ||
		components.VetRepository == nil ||
		components.WelcomeController == nil ||
		components.Renderer == nil ||
		components.Mux == nil {
		t.Fatalf("components = %#v", components)
	}
	if application.State() != lifecycle.StateConstructed {
		t.Fatalf("state = %s", application.State())
	}
	if application.ShutdownTimeout() != 10*time.Second {
		t.Fatalf("shutdown timeout = %s", application.ShutdownTimeout())
	}
	if err := application.RegisterObserver(func(
		context.Context,
		lifecycle.Observation,
	) {
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedPetclinicNilAndConfigurationBoundaries(t *testing.T) {
	t.Parallel()

	var application *Application
	if application.State() != lifecycle.StateInvalid ||
		application.ShutdownTimeout() != 0 ||
		application.Handler() != nil ||
		application.Components().Mux != nil {
		t.Fatal("nil application accessors were not safe")
	}
	if err := application.Start(t.Context()); err == nil {
		t.Fatal("nil Start() succeeded")
	}
	if err := application.Stop(t.Context()); err == nil {
		t.Fatal("nil Stop() succeeded")
	}
	if err := application.RegisterObserver(func(
		context.Context,
		lifecycle.Observation,
	) {
	}); err == nil {
		t.Fatal("nil RegisterObserver() succeeded")
	}
	if err := application.Run(
		t.Context(),
		func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	); err == nil {
		t.Fatal("nil Run() succeeded")
	}
	if _, err := NewApplication(nil); err == nil { //nolint:staticcheck // verifies generated boundary
		t.Fatal("NewApplication(nil) succeeded")
	}
	if _, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{
			Logger:    testLogger(),
			Observers: []lifecycle.Observer{nil},
		},
	); err == nil {
		t.Fatal("nil lifecycle observer succeeded")
	}
	if _, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{
			Logger:        testLogger(),
			HTTPObservers: []web.HTTPObserver{nil},
		},
	); err == nil {
		t.Fatal("nil HTTP observer succeeded")
	}
	source, err := config.NewMapSource("invalid", map[string]string{
		"spice.shutdown-timeout": "0s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if constructed, constructErr := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{
			Sources: []config.Source{source},
			Logger:  testLogger(),
		},
	); constructErr == nil {
		if constructed != nil {
			if stopErr := constructed.Stop(t.Context()); stopErr != nil {
				t.Errorf("Stop() error = %v", stopErr)
			}
		}
		t.Fatal("zero shutdown timeout succeeded")
	}
}

func TestGeneratedPetclinicCommandCheckAndFailures(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if exit := RunCommand(CommandOptions{
		Context:   t.Context(),
		Arguments: []string{"-check"},
		Stdout:    &stdout,
		Logger:    testLogger(),
	}); exit != ExitSuccess {
		t.Fatalf("check exit = %d", exit)
	}
	if strings.TrimSpace(stdout.String()) != "Spice petclinic ready." {
		t.Fatalf("stdout = %q", stdout.String())
	}

	invalid, err := config.NewMapSource("invalid-command", map[string]string{
		"spice.shutdown-timeout": "invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		want int
		opts CommandOptions
	}{
		{name: "nil context", want: ExitFailure},
		{
			name: "negative timeout",
			want: ExitFailure,
			opts: CommandOptions{
				Context:         t.Context(),
				ShutdownTimeout: -time.Second,
			},
		},
		{
			name: "unknown flag",
			want: ExitUsage,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-unknown"},
			},
		},
		{
			name: "positional",
			want: ExitUsage,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"unexpected"},
			},
		},
		{
			name: "invalid configuration",
			want: ExitFailure,
			opts: CommandOptions{
				Context: t.Context(),
				Application: ApplicationOptions{
					Sources: []config.Source{invalid},
				},
			},
		},
		{
			name: "invalid shutdown factory",
			want: ExitFailure,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-check"},
				ShutdownContext: func(
					time.Duration,
				) (context.Context, context.CancelFunc) {
					return nil, func() {}
				},
			},
		},
		{
			name: "failed output",
			want: ExitFailure,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-check"},
				Stdout:    errorWriter{},
			},
		},
	}
	for _, test := range tests {
		test.opts.Logger = testLogger()
		if exit := RunCommand(test.opts); exit != test.want {
			t.Fatalf("%s exit = %d, want %d", test.name, exit, test.want)
		}
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type errorResponseWriter struct {
	header http.Header
}

func (writer *errorResponseWriter) Header() http.Header {
	return writer.header
}

func (*errorResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("write response failed")
}

func (*errorResponseWriter) WriteHeader(int) {}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func servePetclinic(
	handler http.Handler,
	method string,
	target string,
	form url.Values,
) *httptest.ResponseRecorder {
	var request *http.Request
	if form == nil {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(
			method,
			target,
			strings.NewReader(form.Encode()),
		)
		request.Header.Set(
			"Content-Type",
			"application/x-www-form-urlencoded",
		)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func validOwnerForm(firstName, lastName string) url.Values {
	return url.Values{
		"firstName": {firstName},
		"lastName":  {lastName},
		"address":   {"One Main Street"},
		"city":      {"Madison"},
		"telephone": {"6085550100"},
	}
}
