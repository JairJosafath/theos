// The Lambda function the stack builds into bootstrap: wiring only, the work
// is in answer.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func main() {
	// Lambda sends what the function writes to CloudWatch Logs, one entry a
	// line.
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	table := os.Getenv("TABLE_NAME")
	if table == "" {
		log.Error("TABLE_NAME is not set; the stack sets it to the table the function uses.")
		os.Exit(1)
	}
	// Created once per execution environment, before the first request, so
	// warm invocations reuse the client and its connections.
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Error("Loading the AWS configuration failed.", "error", err.Error())
		os.Exit(1)
	}
	store := NewDynamoItemStore(dynamodb.NewFromConfig(cfg), table)
	lambda.Start(func(ctx context.Context, req *events.APIGatewayV2HTTPRequest) (Response, error) {
		return answer(ctx, req, store, log), nil
	})
}
