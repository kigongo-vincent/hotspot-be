package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/config"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/lmittmann/tint"
)

var TestApp *fiber.App
var URL = "/api/v1"
var BEARER = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE3ODg1OTcwMTgsInJvbGUiOiJkZWZhdWx0IiwidWlkIjoxfQ.9iKxJh3cQKLF6FZsII7hC0wfYlaQcTOzCa2T5K4KiGo"

func BASE_URL(v string) string {
	return fmt.Sprintf("%s/%s", URL, v)
}

func ADD_BEARER(reqAddr *http.Request) {
	reqAddr.Header.Set("Authorization", fmt.Sprintf("Bearer %s", BEARER))
}

func ADD_HOST(reqAddr *http.Request) {
	reqAddr.Host = "localhost"
}

func TestMain(m *testing.M) {
	TestApp = config.SetupApplication()
	db := config.ConnectDB()
	config.RegisterAppRoutes(config.Dependencies{DB: db, App: TestApp})
	code := m.Run()
	os.Exit(code)
}

func TestHealth(t *testing.T) {
	t.Skip()
	HTTP[any, any](HTTPRequest[any, any]{Method: fiber.MethodGet, Path: "health"})
	t.Error()
}

func ParseBody[T any](r *http.Response) (base.APIResponseT[T], error, []byte) {

	dataBytes, bytesErr := io.ReadAll(r.Body)
	var err error
	var response base.APIResponseT[T]
	if bytesErr != nil {
		return base.APIResponseT[T]{}, bytesErr, dataBytes
	}
	err = json.Unmarshal(dataBytes, &response)
	if err != nil {
		return base.APIResponseT[T]{}, err, dataBytes
	}
	return response, err, dataBytes

}

type HTTPRequest[T any, U any] struct {
	Method string
	Path   string
	Body   U
}

func HTTP[T any, U any](r HTTPRequest[T, U]) ([]byte, *base.APIResponseT[T], error) {

	// stringify the body
	data, jsonErr := json.Marshal(r.Body)
	if jsonErr != nil {
		return nil, &base.APIResponseT[T]{Status: 1}, nil
	}

	req, reqErr := http.NewRequest(r.Method, BASE_URL(r.Path), bytes.NewBuffer(data))
	req.Header.Set("Content-type", "application/json")
	ADD_BEARER(req)
	ADD_HOST(req)

	if reqErr != nil {
		return nil, &base.APIResponseT[T]{Status: 2}, reqErr
	}

	res, resErr := TestApp.Test(req)
	if resErr != nil {
		return nil, &base.APIResponseT[T]{Status: 3}, resErr
	}
	defer res.Body.Close()

	body, bodyErr, raw := ParseBody[T](res)
	if bodyErr != nil {
		return raw, &base.APIResponseT[T]{Status: 4}, bodyErr
	}
	body.Status = res.StatusCode

	handler := tint.NewHandler(os.Stdout, &tint.Options{
		Level:      slog.LevelDebug,
		TimeFormat: time.RFC3339,
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Colorful Log Examples
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("|	STATUS: ", res.StatusCode)
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("|	METHOD: ", req.Method)
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("|	PATH: ", req.URL.Path)
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("|	RESPONSE: ")
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("	" + string(raw))
	fmt.Println("-----------------------------------------------------------------------------------")
	fmt.Println("-----------------------------------------------------------------------------------")

	return raw, &body, nil
}

func GenerateRequest[T any](t *testing.T, method string, url string, data T) *http.Request {
	dataBytes, dataBytesErr := json.Marshal(data)
	if dataBytesErr != nil {
		t.Error(dataBytesErr)
	}
	req, reqErr := http.NewRequest(method, BASE_URL(url), bytes.NewBuffer(dataBytes))
	if reqErr != nil {
		t.Error(reqErr)
	}
	return req
}
