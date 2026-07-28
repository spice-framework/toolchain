package spicegen

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	spiceasync "github.com/StevenBuglione/spice/async"
	"github.com/StevenBuglione/spice/config"
	"github.com/StevenBuglione/spice/examples/commerce/orders"
	"github.com/StevenBuglione/spice/lifecycle"
	"github.com/StevenBuglione/spice/security"
	"github.com/StevenBuglione/spice/spicetest"
	"github.com/StevenBuglione/spice/web"
)

func TestGeneratedApplicationConstructsTypedComponentsAndStops(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{Logger: generatedTestLogger()},
	)
	if err != nil {
		t.Fatalf("NewApplicationWithOptions() error = %v", err)
	}
	components := application.Components()
	if components.StripeProcessor == nil ||
		components.OfflineProcessor == nil ||
		components.OrdersService == nil ||
		components.OrderRepository == nil ||
		components.Delivery == nil {
		t.Fatal("Components() has missing required singleton beans")
	}
	if application.State() != lifecycle.StateConstructed {
		t.Fatalf("State() = %s, want constructed", application.State())
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if application.State() != lifecycle.StateStopped {
		t.Fatalf("State() = %s, want stopped", application.State())
	}
}

func TestGeneratedDefaultConstructorExposesLifecycleObservation(t *testing.T) {
	t.Parallel()

	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if err := application.RegisterObserver(
		func(context.Context, lifecycle.Observation) {},
	); err != nil {
		t.Fatalf("RegisterObserver() error = %v", err)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestGeneratedConfigurationBindsEveryOwnedComponent(t *testing.T) {
	t.Parallel()

	overrides, err := config.NewMapSource("complete-generated-configuration", map[string]string{
		"commerce.orders.sku":                   "SKU-BLUE",
		"commerce.orders.unit-price-cents":      "4100",
		"commerce.database.url":                 "memory://commerce",
		"commerce.database.allow-insecure":      "true",
		"commerce.payments.maximum-cents":       "900000",
		"commerce.server.address":               "127.0.0.1:0",
		"commerce.server.read-header-timeout":   "2s",
		"commerce.server.read-timeout":          "3s",
		"commerce.server.write-timeout":         "4s",
		"commerce.server.idle-timeout":          "5s",
		"commerce.server.developer-token":       "coverage-token-value",
		"commerce.inventory.sku":                "SKU-BLUE",
		"commerce.inventory.initial-stock":      "25",
		"commerce.mail.transport":               "test",
		"commerce.mail.from":                    "Coverage <coverage@example.test>",
		"commerce.mail.recipient":               "Developer <developer@example.test>",
		"commerce.mail.test-capacity":           "7",
		"commerce.mail.smtp-address":            "smtp.example.test:587",
		"commerce.mail.smtp-server-name":        "smtp.example.test",
		"commerce.mail.smtp-mode":               "starttls",
		"commerce.mail.smtp-username":           "coverage-user",
		"commerce.mail.smtp-password":           "coverage-password",
		"commerce.mail.timeout":                 "6s",
		"commerce.mail.max-attempts":            "2",
		"spice.cache.commerce.catalog.capacity": "17",
		"spice.cache.commerce.catalog.ttl":      "30s",
		"spice.async.max-concurrency":           "3",
		"spice.shutdown-timeout":                "7s",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource() error = %v", err)
	}
	application, err := NewApplicationWithOptions(context.Background(), ApplicationOptions{
		Sources: []config.Source{overrides},
		Logger:  generatedTestLogger(),
	})
	if err != nil {
		t.Fatalf("NewApplicationWithOptions() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := application.Stop(context.Background()); closeErr != nil {
			t.Errorf("Stop() error = %v", closeErr)
		}
	})

	components := application.Components()
	if components.OrdersSettings.SKU != "SKU-BLUE" ||
		components.OrdersSettings.UnitPriceCents != 4100 {
		t.Fatalf("OrdersSettings = %#v", components.OrdersSettings)
	}
	if components.StorageSettings.URL != "memory://commerce" ||
		!components.StorageSettings.AllowInsecure {
		t.Fatalf("StorageSettings = %#v", components.StorageSettings)
	}
	if components.PaymentsSettings.MaximumCents != 900000 {
		t.Fatalf("PaymentsSettings = %#v", components.PaymentsSettings)
	}
	if components.PlatformSettings.Address != "127.0.0.1:0" ||
		components.PlatformSettings.ReadHeaderTimeout != 2*time.Second ||
		components.PlatformSettings.ReadTimeout != 3*time.Second ||
		components.PlatformSettings.WriteTimeout != 4*time.Second ||
		components.PlatformSettings.IdleTimeout != 5*time.Second ||
		components.PlatformSettings.DeveloperToken != "coverage-token-value" {
		t.Fatalf("PlatformSettings = %#v", components.PlatformSettings)
	}
	if components.InventorySettings.SKU != "SKU-BLUE" ||
		components.InventorySettings.InitialStock != 25 {
		t.Fatalf("InventorySettings = %#v", components.InventorySettings)
	}
	if components.NotificationsSettings.Transport != "test" ||
		components.NotificationsSettings.TestCapacity != 7 ||
		components.NotificationsSettings.Timeout != 6*time.Second ||
		components.NotificationsSettings.MaxAttempts != 2 {
		t.Fatalf("NotificationsSettings = %#v", components.NotificationsSettings)
	}
	if application.ShutdownTimeout() != 7*time.Second {
		t.Fatalf(
			"ShutdownTimeout() = %s, want 7s",
			application.ShutdownTimeout(),
		)
	}
}

func TestGeneratedConfigurationRejectsInvalidOwnedValues(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		key       string
		value     string
		wantError string
	}{
		"shutdown duration": {
			key:       "spice.shutdown-timeout",
			value:     "0s",
			wantError: "duration must be positive",
		},
		"orders integer": {
			key:       "commerce.orders.unit-price-cents",
			value:     "not-an-integer",
			wantError: "unit-price-cents",
		},
		"database Boolean": {
			key:       "commerce.database.allow-insecure",
			value:     "sometimes",
			wantError: "allow-insecure",
		},
		"server duration": {
			key:       "commerce.server.read-timeout",
			value:     "not-a-duration",
			wantError: "read-timeout",
		},
		"async concurrency": {
			key:       "spice.async.max-concurrency",
			value:     "0",
			wantError: "positive int",
		},
		"cache capacity": {
			key:       "spice.cache.commerce.catalog.capacity",
			value:     "0",
			wantError: "positive int",
		},
		"cache TTL": {
			key:       "spice.cache.commerce.catalog.ttl",
			value:     "-1s",
			wantError: "must not be negative",
		},
	} {
		source, err := config.NewMapSource(name, map[string]string{
			test.key: test.value,
		})
		if err != nil {
			t.Fatalf("%s: config.NewMapSource() error = %v", name, err)
		}
		application, constructErr := NewApplicationWithOptions(
			context.Background(),
			ApplicationOptions{
				Sources: []config.Source{source},
				Logger:  generatedTestLogger(),
			},
		)
		if constructErr == nil {
			if application != nil {
				if stopErr := application.Stop(context.Background()); stopErr != nil {
					t.Errorf("%s: Stop() error = %v", name, stopErr)
				}
			}
			t.Fatalf("%s: NewApplicationWithOptions() error = nil", name)
		}
		if !strings.Contains(constructErr.Error(), test.wantError) {
			t.Fatalf(
				"%s: NewApplicationWithOptions() error = %q, want %q",
				name,
				constructErr,
				test.wantError,
			)
		}
	}
}

func TestGeneratedCommandCheckUsesReusablePackageSurface(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := RunCommand(CommandOptions{
		Context:   context.Background(),
		Arguments: []string{"-check"},
		Stdout:    &stdout,
		Stderr:    &stderr,
		Logger:    generatedTestLogger(),
	})
	if exitCode != ExitSuccess {
		t.Fatalf(
			"RunCommand() exit = %d, stderr=%q",
			exitCode,
			stderr.String(),
		)
	}
	if strings.TrimSpace(stdout.String()) != "Spice commerce ready." {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestGeneratedCommandReportsInvalidInputsAndCleanupFailures(t *testing.T) {
	t.Parallel()

	invalidConfiguration, err := config.NewMapSource("invalid-command", map[string]string{
		"spice.shutdown-timeout": "not-a-duration",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource() error = %v", err)
	}
	for name, options := range map[string]CommandOptions{
		"nil context": {},
		"negative shutdown": {
			Context:         context.Background(),
			ShutdownTimeout: -time.Second,
		},
		"unknown flag": {
			Context:   context.Background(),
			Arguments: []string{"-unknown"},
		},
		"positional argument": {
			Context:   context.Background(),
			Arguments: []string{"unexpected"},
		},
		"invalid configuration": {
			Context: context.Background(),
			Application: ApplicationOptions{
				Sources: []config.Source{invalidConfiguration},
			},
		},
		"invalid check shutdown factory": {
			Context:   context.Background(),
			Arguments: []string{"-check"},
			ShutdownContext: func(
				time.Duration,
			) (context.Context, context.CancelFunc) {
				return nil, func() {}
			},
		},
		"failed readiness output": {
			Context:   context.Background(),
			Arguments: []string{"-check"},
			Stdout:    generatedErrorWriter{},
		},
	} {
		options.Logger = generatedTestLogger()
		exitCode := RunCommand(options)
		want := ExitFailure
		if name == "unknown flag" || name == "positional argument" {
			want = ExitUsage
		}
		if exitCode != want {
			t.Fatalf("%s: RunCommand() = %d, want %d", name, exitCode, want)
		}
	}
}

func TestGeneratedCommandRunsCompletePackageLifecycle(t *testing.T) {
	t.Parallel()

	overrides, err := config.NewMapSource("command-run", map[string]string{
		"commerce.server.address": "127.0.0.1:0",
		"spice.shutdown-timeout":  "1s",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
			cancel()
		}
	}()
	exitCode := RunCommand(CommandOptions{
		Context:         ctx,
		Logger:          generatedTestLogger(),
		ShutdownTimeout: time.Second,
		Application: ApplicationOptions{
			Sources: []config.Source{overrides},
		},
	})
	cancel()
	<-cancelDone
	if exitCode != ExitSuccess {
		t.Fatalf("RunCommand() = %d, want %d", exitCode, ExitSuccess)
	}
}

func TestGeneratedHTTPAdaptersExecuteThroughTypedTestSlice(t *testing.T) {
	t.Parallel()

	server, err := spicetest.NewHTTP(
		context.Background(),
		func(ctx context.Context) (spicetest.HTTPApplication, error) {
			return NewApplicationWithOptions(
				ctx,
				ApplicationOptions{Logger: generatedTestLogger()},
			)
		},
		spicetest.HTTPOptions{ShutdownTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("spicetest.NewHTTP() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	response, err := server.Do(context.Background(), spicetest.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/catalog",
	})
	if err != nil {
		t.Fatalf("GET /catalog error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"GET /catalog status = %d, body=%s",
			response.StatusCode,
			response.Body,
		)
	}
	var catalog struct {
		SKU            string `json:"sku"`
		UnitPriceCents int    `json:"unit_price_cents"`
	}
	if decodeErr := response.DecodeJSON(&catalog); decodeErr != nil {
		t.Fatalf("decode catalog: %v", decodeErr)
	}
	if catalog.SKU != "SKU-RED" || catalog.UnitPriceCents != 2500 {
		t.Fatalf("catalog = %#v", catalog)
	}

	response, err = server.Do(context.Background(), spicetest.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/actuator/info",
	})
	if err != nil {
		t.Fatalf("GET /actuator/info error = %v", err)
	}
	if response.StatusCode != http.StatusOK ||
		!json.Valid(response.Body) {
		t.Fatalf(
			"GET /actuator/info status=%d body=%s",
			response.StatusCode,
			response.Body,
		)
	}
}

func TestGeneratedHTTPAdaptersExecuteAuthenticatedCommerceWorkflow(
	t *testing.T,
) {
	t.Parallel()

	principal, err := security.NewPrincipal(
		"generated-test",
		"spice://generated-test",
		nil,
		[]string{"orders:notify", "orders:read", "orders:write"},
	)
	if err != nil {
		t.Fatalf("security.NewPrincipal() error = %v", err)
	}
	authentication := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(
			writer http.ResponseWriter,
			request *http.Request,
		) {
			ctx, attachErr := security.WithPrincipal(
				request.Context(),
				principal,
			)
			if attachErr != nil {
				http.Error(
					writer,
					"authentication unavailable",
					http.StatusInternalServerError,
				)
				return
			}
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
	server, err := spicetest.NewHTTP(
		context.Background(),
		func(ctx context.Context) (spicetest.HTTPApplication, error) {
			return NewApplicationWithOptions(ctx, ApplicationOptions{
				Logger:     generatedTestLogger(),
				Middleware: []web.Middleware{authentication},
			})
		},
		spicetest.HTTPOptions{ShutdownTimeout: time.Second},
	)
	if err != nil {
		t.Fatalf("spicetest.NewHTTP() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	response, err := server.Do(context.Background(), spicetest.HTTPRequest{
		Method: http.MethodPost,
		Path:   "/orders",
		JSON:   map[string]int{"quantity": 2},
	})
	if err != nil {
		t.Fatalf("POST /orders error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"POST /orders status = %d, body=%s",
			response.StatusCode,
			response.Body,
		)
	}
	var placed orders.OrderResponse
	if decodeErr := response.DecodeJSON(&placed); decodeErr != nil {
		t.Fatalf("decode placed order: %v", decodeErr)
	}
	if placed.ID == "" || placed.Quantity != 2 {
		t.Fatalf("placed order = %#v", placed)
	}

	response, err = server.Do(context.Background(), spicetest.HTTPRequest{
		Method: http.MethodGet,
		Path:   "/orders/" + placed.ID,
	})
	if err != nil {
		t.Fatalf("GET placed order error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"GET placed order status = %d, body=%s",
			response.StatusCode,
			response.Body,
		)
	}

	response, err = server.Do(context.Background(), spicetest.HTTPRequest{
		Method: http.MethodPost,
		Path:   "/orders/" + placed.ID + "/receipt",
	})
	if err != nil {
		t.Fatalf("POST receipt error = %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"POST receipt status = %d, body=%s",
			response.StatusCode,
			response.Body,
		)
	}
	var receipt orders.ReceiptResponse
	if decodeErr := response.DecodeJSON(&receipt); decodeErr != nil {
		t.Fatalf("decode receipt: %v", decodeErr)
	}
	if !receipt.Accepted || receipt.Transport != "test" {
		t.Fatalf("receipt = %#v", receipt)
	}

	for range 2 {
		response, err = server.Do(context.Background(), spicetest.HTTPRequest{
			Method: http.MethodGet,
			Path:   "/catalog",
		})
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf(
				"GET /catalog error=%v status=%d body=%s",
				err,
				response.StatusCode,
				response.Body,
			)
		}
	}

	for name, request := range map[string]spicetest.HTTPRequest{
		"invalid order": {
			Method: http.MethodPost,
			Path:   "/orders",
			JSON:   map[string]int{"quantity": 0},
		},
		"missing order": {
			Method: http.MethodGet,
			Path:   "/orders/missing",
		},
		"not acceptable": {
			Method: http.MethodGet,
			Path:   "/catalog",
			Header: http.Header{"Accept": []string{"text/plain"}},
		},
		"not acceptable order": {
			Method: http.MethodGet,
			Path:   "/orders/" + placed.ID,
			Header: http.Header{"Accept": []string{"text/plain"}},
		},
		"not acceptable placement": {
			Method: http.MethodPost,
			Path:   "/orders",
			Header: http.Header{"Accept": []string{"text/plain"}},
		},
		"not acceptable receipt": {
			Method: http.MethodPost,
			Path:   "/orders/" + placed.ID + "/receipt",
			Header: http.Header{"Accept": []string{"text/plain"}},
		},
		"malformed order JSON": {
			Method: http.MethodPost,
			Path:   "/orders",
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: []byte("["),
		},
		"missing order receipt": {
			Method: http.MethodPost,
			Path:   "/orders/missing/receipt",
		},
	} {
		response, requestErr := server.Do(
			context.Background(),
			request,
		)
		if requestErr != nil {
			t.Fatalf("%s request error = %v", name, requestErr)
		}
		if response.StatusCode < http.StatusBadRequest {
			t.Fatalf(
				"%s status = %d, body=%s",
				name,
				response.StatusCode,
				response.Body,
			)
		}
		if _, problemErr := response.Problem(); problemErr != nil {
			t.Fatalf("%s problem response: %v", name, problemErr)
		}
	}
}

func TestGeneratedApplicationContextRunsLifecycleAndAsyncWork(t *testing.T) {
	t.Parallel()

	overrides, err := config.NewMapSource("test", map[string]string{
		"commerce.server.address": "127.0.0.1:0",
		"spice.shutdown-timeout":  "1s",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource() error = %v", err)
	}
	asyncResults := make(chan spiceasync.Result, 1)
	testContext, err := spicetest.NewContext(
		context.Background(),
		func(ctx context.Context) (*Application, error) {
			return NewApplicationWithOptions(ctx, ApplicationOptions{
				Sources: []config.Source{overrides},
				Logger:  generatedTestLogger(),
				AsyncObservers: []spiceasync.Observer{
					func(_ context.Context, result spiceasync.Result) {
						asyncResults <- result
					},
				},
			})
		},
		spicetest.ContextOptions{ShutdownTimeout: 2 * time.Second},
	)
	if err != nil {
		t.Fatalf("spicetest.NewContext() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := testContext.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	application := testContext.Application()
	if application.State() != lifecycle.StateReady {
		t.Fatalf("State() = %s, want ready", application.State())
	}
	if err := application.SubmitServiceVerifySKU(
		context.Background(),
		"SKU-RED",
	); err != nil {
		t.Fatalf("SubmitServiceVerifySKU() error = %v", err)
	}
	if err := testContext.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case result := <-asyncResults:
		if result.Err != nil || result.Panicked {
			t.Fatalf("asynchronous result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("asynchronous result was not observed")
	}
	if snapshot := application.AsyncSnapshot(); snapshot.Submitted != 1 ||
		snapshot.Completed != 1 ||
		!snapshot.Closed {
		t.Fatalf("AsyncSnapshot() = %#v", snapshot)
	}
}

func TestGeneratedApplicationRejectsInvalidReusableCalls(t *testing.T) {
	t.Parallel()

	var application *Application
	if application.State() != lifecycle.StateInvalid ||
		application.ShutdownTimeout() != 0 ||
		application.Handler() != nil ||
		application.Components() != (Components{}) ||
		!application.AsyncSnapshot().Closed {
		t.Fatal("nil generated application accessors returned unsafe values")
	}
	for name, operation := range map[string]func() error{
		"start": func() error {
			return application.Start(context.Background())
		},
		"stop": func() error {
			return application.Stop(context.Background())
		},
		"observer": func() error {
			return application.RegisterObserver(
				func(context.Context, lifecycle.Observation) {},
			)
		},
		"run": func() error {
			return application.Run(
				context.Background(),
				func() (context.Context, context.CancelFunc) {
					return context.WithCancel(context.Background())
				},
			)
		},
		"async": func() error {
			return application.SubmitServiceVerifySKU(
				context.Background(),
				"SKU-RED",
			)
		},
	} {
		if err := operation(); err == nil {
			t.Fatalf("%s on nil application unexpectedly succeeded", name)
		}
	}
}

func generatedTestLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

type generatedErrorWriter struct{}

func (generatedErrorWriter) Write([]byte) (int, error) {
	return 0, context.Canceled
}
