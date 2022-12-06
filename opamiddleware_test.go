// Copyright 2021 Mia srl
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rond-authz/rond/internal/config"
	"github.com/rond-authz/rond/types"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

var envs = config.EnvironmentVariables{}

var partialEvaluators = PartialResultsEvaluators{}

func TestOPAMiddleware(t *testing.T) {
	t.Run(`strict mode failure`, func(t *testing.T) {
		opaModule := &OPAModuleConfig{
			Name: "example.rego",
			Content: `package policies
todo { true }`,
		}
		var openAPISpec *OpenAPISpec
		openAPISpecContent, _ := os.ReadFile("./mocks/simplifiedMock.json")
		_ = json.Unmarshal(openAPISpecContent, &openAPISpec)
		middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)

		t.Run(`missing oas paths`, func(t *testing.T) {
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fail()
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/not-existing-path", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusNotFound, w.Result().StatusCode, "Unexpected status code.")
			require.Equal(t, &types.RequestError{
				Message:    "The request doesn't match any known API",
				Error:      "not found oas definition: GET /not-existing-path",
				StatusCode: http.StatusNotFound,
			}, getJSONResponseBody[types.RequestError](t, w))
			require.Equal(t, JSONContentTypeHeader, w.Result().Header.Get(ContentTypeHeaderKey), "Unexpected content type.")
		})

		t.Run(`missing method`, func(t *testing.T) {
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fail()
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodDelete, "http://example.com/users/", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusNotFound, w.Result().StatusCode, "Unexpected status code.")
			require.Equal(t, &types.RequestError{
				Message:    "The request doesn't match any known API",
				Error:      "not found oas definition: DELETE /users/",
				StatusCode: http.StatusNotFound,
			}, getJSONResponseBody[types.RequestError](t, w))
			require.Equal(t, JSONContentTypeHeader, w.Result().Header.Get(ContentTypeHeaderKey), "Unexpected content type.")
		})

		t.Run(`missing permission`, func(t *testing.T) {
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fail()
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "http://example.com/no-permission", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusForbidden, w.Result().StatusCode, "Unexpected status code.")
		})
	})

	t.Run(`documentation request`, func(t *testing.T) {
		opaModule := &OPAModuleConfig{
			Name: "example.rego",
			Content: `package policies
foobar { true }`,
		}

		t.Run(`ok - path is known on oas with no permission declared`, func(t *testing.T) {
			openAPISpec, err := loadOASFile("./mocks/documentationPathMock.json")
			require.NoError(t, err)
			var envs = config.EnvironmentVariables{
				TargetServiceOASPath: "/documentation/json",
			}
			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "http://example.com/documentation/json", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})

		t.Run(`ok - path is missing on oas and request is equal to serviceTargetOASPath`, func(t *testing.T) {
			openAPISpec, err := loadOASFile("./mocks/simplifiedMock.json")
			require.NoError(t, err)
			var envs = config.EnvironmentVariables{
				TargetServiceOASPath: "/documentation/json",
			}
			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/documentation/json", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})

		t.Run(`ok - path is NOT known on oas but is proxied anyway`, func(t *testing.T) {
			openAPISpec, err := loadOASFile("./mocks/simplifiedMock.json")
			require.NoError(t, err)
			var envs = config.EnvironmentVariables{
				TargetServiceOASPath: "/documentation/custom/json",
			}
			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/documentation/custom/json", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})
	})

	t.Run(`injects opa instance with correct query`, func(t *testing.T) {
		openAPISpec, err := loadOASFile("./mocks/simplifiedMock.json")
		require.NoError(t, err)

		t.Run(`rego package doesn't contain expected permission`, func(t *testing.T) {
			opaModule := &OPAModuleConfig{
				Name: "example.rego",
				Content: `package policies
todo { true }`,
			}

			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				permission, err := GetXPermission(r.Context())
				require.True(t, err == nil, "Unexpected error")
				require.Equal(t, permission, &RondConfig{RequestFlow: RequestFlow{PolicyName: "todo"}})
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/users/", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})

		t.Run(`rego package contains expected permission`, func(t *testing.T) {
			opaModule := &OPAModuleConfig{
				Name: "example.rego",
				Content: `package policies
foobar { true }`,
			}

			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				permission, err := GetXPermission(r.Context())
				require.True(t, err == nil, "Unexpected error")
				require.Equal(t, &RondConfig{RequestFlow: RequestFlow{PolicyName: "todo"}}, permission)
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/users/", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})

		t.Run(`rego package contains composed permission`, func(t *testing.T) {
			opaModule := &OPAModuleConfig{
				Name: "example.rego",
				Content: `package policies
very_very_composed_permission { true }`,
			}

			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				permission, err := GetXPermission(r.Context())
				require.True(t, err == nil, "Unexpected error")
				require.Equal(t, &RondConfig{RequestFlow: RequestFlow{PolicyName: "very.very.composed.permission"}}, permission)
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/composed/permission/", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})

		t.Run("injects correct permission", func(t *testing.T) {
			opaModule := &OPAModuleConfig{
				Name: "example.rego",
				Content: `package policies
very_very_composed_permission_with_eval { true }`,
			}

			envs := config.EnvironmentVariables{
				Standalone:           false,
				PathPrefixStandalone: "/eval", // default value
			}

			middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
			builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				permission, err := GetXPermission(r.Context())
				require.True(t, err == nil, "Unexpected error")
				require.Equal(t, &RondConfig{RequestFlow: RequestFlow{PolicyName: "very.very.composed.permission.with.eval"}}, permission)
				w.WriteHeader(http.StatusOK)
			}))

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "http://example.com/eval/composed/permission/", nil)
			builtHandler.ServeHTTP(w, r)

			require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
		})
	})
}

func TestOPAMiddlewareStandaloneIntegration(t *testing.T) {
	openAPISpec, err := loadOASFile("./mocks/simplifiedMock.json")
	require.Nil(t, err)

	envs := config.EnvironmentVariables{
		Standalone:           true,
		PathPrefixStandalone: "/eval", // default value
	}

	t.Run("injects correct path removing prefix", func(t *testing.T) {
		opaModule := &OPAModuleConfig{
			Name: "example.rego",
			Content: `package policies
			very_very_composed_permission { true }`,
		}

		middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
		builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			permission, err := GetXPermission(r.Context())
			require.True(t, err == nil, "Unexpected error")
			require.Equal(t, &RondConfig{RequestFlow: RequestFlow{PolicyName: "very.very.composed.permission"}}, permission)
			w.WriteHeader(http.StatusOK)
		}))

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.com/eval/composed/permission/", nil)
		builtHandler.ServeHTTP(w, r)

		require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
	})

	t.Run("injects correct path removing only one prefix", func(t *testing.T) {
		opaModule := &OPAModuleConfig{
			Name: "example.rego",
			Content: `package policies
very_very_composed_permission_with_eval { true }`,
		}

		middleware := OPAMiddleware(opaModule, openAPISpec, &envs, partialEvaluators)
		builtHandler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			permission, err := GetXPermission(r.Context())
			require.True(t, err == nil, "Unexpected error")
			require.Equal(t, &RondConfig{RequestFlow: RequestFlow{PolicyName: "very.very.composed.permission.with.eval"}}, permission)
			w.WriteHeader(http.StatusOK)
		}))

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://example.com/eval/eval/composed/permission/", nil)
		builtHandler.ServeHTTP(w, r)

		require.Equal(t, http.StatusOK, w.Result().StatusCode, "Unexpected status code.")
	})
}

func TestGetHeaderFunction(t *testing.T) {
	headerKeyMocked := "exampleKey"
	headerValueMocked := "value"
	env := config.EnvironmentVariables{}

	opaModule := &OPAModuleConfig{
		Name: "example.rego",
		Content: `package policies
		todo { get_header("ExAmPlEkEy", input.headers) == "value" }`,
	}
	queryString := "todo"

	t.Run("if header key exists", func(t *testing.T) {
		headers := http.Header{}
		headers.Add(headerKeyMocked, headerValueMocked)
		input := map[string]interface{}{
			"headers": headers,
		}
		inputBytes, _ := json.Marshal(input)

		opaEvaluator, err := NewOPAEvaluator(context.Background(), queryString, opaModule, inputBytes, env)
		require.NoError(t, err, "Unexpected error during creation of opaEvaluator")

		results, err := opaEvaluator.PolicyEvaluator.Eval(context.TODO())
		require.NoError(t, err, "Unexpected error during rego validation")
		require.True(t, results.Allowed(), "The input is not allowed by rego")

		partialResults, err := opaEvaluator.PolicyEvaluator.Partial(context.TODO())
		require.NoError(t, err, "Unexpected error during rego validation")

		require.Len(t, partialResults.Queries, 1, "Rego policy allows illegal input")
	})

	t.Run("if header key not exists", func(t *testing.T) {
		input := map[string]interface{}{
			"headers": http.Header{},
		}
		inputBytes, _ := json.Marshal(input)

		opaEvaluator, err := NewOPAEvaluator(context.Background(), queryString, opaModule, inputBytes, env)
		require.NoError(t, err, "Unexpected error during creation of opaEvaluator")

		results, err := opaEvaluator.PolicyEvaluator.Eval(context.TODO())
		require.NoError(t, err, "Unexpected error during rego validation")
		require.True(t, !results.Allowed(), "Rego policy allows illegal input")

		partialResults, err := opaEvaluator.PolicyEvaluator.Partial(context.TODO())
		require.NoError(t, err, "Unexpected error during rego validation")

		require.Len(t, partialResults.Queries, 0, "Rego policy allows illegal input")
	})
}

func TestGetOPAModuleConfig(t *testing.T) {
	t.Run(`GetOPAModuleConfig fails because no key has been passed`, func(t *testing.T) {
		ctx := context.Background()
		env, err := GetOPAModuleConfig(ctx)
		require.True(t, err != nil, "An error was expected.")
		t.Logf("Expected error: %s - env: %+v", err.Error(), env)
	})

	t.Run(`GetOPAModuleConfig returns OPAEvaluator from context`, func(t *testing.T) {
		ctx := context.WithValue(context.Background(), OPAModuleConfigKey{}, &OPAModuleConfig{})
		opaEval, err := GetOPAModuleConfig(ctx)
		require.True(t, err == nil, "Unexpected error.")
		require.True(t, opaEval != nil, "OPA Module config not found.")
	})
}

func TestRouterInfoContext(t *testing.T) {
	nullLogger, _ := test.NewNullLogger()
	logger := logrus.NewEntry(nullLogger)

	t.Run("GetRouterInfo fails because no key has been set", func(t *testing.T) {
		ctx := context.Background()
		routerInfo, err := GetRouterInfo(ctx)
		require.EqualError(t, err, "no router info found")
		require.Empty(t, routerInfo)
	})

	t.Run("WithRouterInfo not inside mux router - empty matched path", func(t *testing.T) {
		ctx := context.Background()
		req := httptest.NewRequest("GET", "/hello", nil)
		ctx = WithRouterInfo(logger, ctx, req)
		routerInfo, err := GetRouterInfo(ctx)
		require.NoError(t, err)
		require.Equal(t, RouterInfo{
			MatchedPath:   "",
			RequestedPath: "/hello",
			Method:        "GET",
		}, routerInfo)
	})

	t.Run("WithRouterInfo without router path - matched path is empty", func(t *testing.T) {
		ctx := context.Background()
		router := mux.NewRouter()

		router.NewRoute().HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := WithRouterInfo(logger, ctx, req)

			routerInfo, err := GetRouterInfo(ctx)
			require.NoError(t, err)
			require.Equal(t, RouterInfo{
				MatchedPath:   "",
				RequestedPath: "/hello",
				Method:        "GET",
			}, routerInfo)

			w.Write([]byte("ok"))
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/hello", nil)
		router.ServeHTTP(w, req)

		require.Equal(t, 200, w.Result().StatusCode)
	})

	t.Run("correctly get router info", func(t *testing.T) {
		ctx := context.Background()
		router := mux.NewRouter()

		router.HandleFunc("/hello/{name}", func(w http.ResponseWriter, req *http.Request) {
			ctx := WithRouterInfo(logger, ctx, req)

			routerInfo, err := GetRouterInfo(ctx)
			require.NoError(t, err)
			require.Equal(t, RouterInfo{
				MatchedPath:   "/hello/{name}",
				RequestedPath: "/hello/my-username",
				Method:        "GET",
			}, routerInfo)

			w.Write([]byte("ok"))
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/hello/my-username", nil)
		router.ServeHTTP(w, req)

		require.Equal(t, 200, w.Result().StatusCode)
	})

	t.Run("correctly get router info with path prefix", func(t *testing.T) {
		ctx := context.Background()
		router := mux.NewRouter()

		router.PathPrefix("/hello/").HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := WithRouterInfo(logger, ctx, req)

			routerInfo, err := GetRouterInfo(ctx)
			require.NoError(t, err)
			require.Equal(t, RouterInfo{
				MatchedPath:   "/hello/",
				RequestedPath: "/hello/my-username",
				Method:        "GET",
			}, routerInfo)

			w.Write([]byte("ok"))
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/hello/my-username", nil)
		router.ServeHTTP(w, req)

		require.Equal(t, 200, w.Result().StatusCode)
	})
}

func getResponseBody(t *testing.T, w *httptest.ResponseRecorder) []byte {
	t.Helper()

	responseBody, err := io.ReadAll(w.Result().Body)
	require.NoError(t, err)

	return responseBody
}

func getJSONResponseBody[T any](t *testing.T, w *httptest.ResponseRecorder) *T {
	t.Helper()

	responseBody := getResponseBody(t, w)
	out := new(T)
	if err := json.Unmarshal(responseBody, out); err != nil {
		require.Error(t, err, "fails to unmarshal")
	}
	return out
}
