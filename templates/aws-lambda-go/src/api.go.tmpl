package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/aws/aws-lambda-go/events"
)

// Response is an HTTP response, in the shape API Gateway's HTTP API reads
// from a Lambda function.
type Response = events.APIGatewayV2HTTPResponse

// answer answers one request of the HTTP API. Every route returns a status
// code a client can act on; an unexpected failure is logged and answered with
// 500, without its details.
func answer(ctx context.Context, req *events.APIGatewayV2HTTPRequest, store ItemStore, log *slog.Logger) Response {
	resp, err := route(ctx, req, store)
	if err != nil {
		log.ErrorContext(ctx, "The request failed.",
			"requestId", req.RequestContext.RequestID,
			"routeKey", req.RouteKey,
			"error", err.Error())
		return problem(http.StatusInternalServerError, "Something went wrong. Try again later.")
	}
	return resp
}

func route(ctx context.Context, req *events.APIGatewayV2HTTPRequest, store ItemStore) (Response, error) {
	id, err := checkID(req.PathParameters["id"])
	if err != nil {
		return problem(http.StatusBadRequest, sentence(err)), nil
	}
	switch req.RouteKey {
	case "GET /items/{id}":
		item, found, err := store.Get(ctx, id)
		if err != nil {
			return Response{}, err
		}
		if !found {
			return problem(http.StatusNotFound, "No item "+id+"."), nil
		}
		return jsonResponse(http.StatusOK, item), nil
	case "PUT /items/{id}":
		item, err := checkItem(id, body(req))
		if err != nil {
			return problem(http.StatusBadRequest, sentence(err)), nil
		}
		// A put replaces the whole item, so repeating a request is harmless.
		if err := store.Put(ctx, item); err != nil {
			return Response{}, err
		}
		return jsonResponse(http.StatusOK, item), nil
	default:
		return problem(http.StatusNotFound, "No route "+req.RouteKey+"."), nil
	}
}

// body is the request's body, decoded when API Gateway sent it in base64.
// A body that isn't base64 reads as empty, which checkItem refuses.
func body(req *events.APIGatewayV2HTTPRequest) string {
	if !req.IsBase64Encoded {
		return req.Body
	}
	decoded, err := base64.StdEncoding.DecodeString(req.Body)
	if err != nil {
		return ""
	}
	return string(decoded)
}

// jsonResponse is a JSON response. value is an Item or a problem's message,
// which always marshal.
func jsonResponse(status int, value any) Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal a response: %v", err))
	}
	return Response{
		StatusCode: status,
		Headers:    map[string]string{"content-type": "application/json"},
		Body:       string(body),
	}
}

// problem is an error response a client can read: the status and one
// message, never a stack trace.
func problem(status int, message string) Response {
	return jsonResponse(status, struct {
		Message string `json:"message"`
	}{message})
}

// sentence makes a rule's error a message for a client: capitalized, with a
// full stop.
func sentence(err error) string {
	msg := err.Error()
	if msg == "" {
		return msg
	}
	if c := msg[0]; c >= 'a' && c <= 'z' {
		msg = string(c-'a'+'A') + msg[1:]
	}
	return msg + "."
}
