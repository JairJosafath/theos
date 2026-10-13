package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo keeps one table's rows in memory and records what was asked;
// err makes every call fail, as an outage would.
type fakeDynamo struct {
	rows  map[string]map[string]types.AttributeValue
	table string
	err   error
}

func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.table = aws.ToString(in.TableName)
	key, ok := in.Key["id"].(*types.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("the key is not a string id")
	}
	return &dynamodb.GetItemOutput{Item: f.rows[key.Value]}, nil
}

func (f *fakeDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.table = aws.ToString(in.TableName)
	key, ok := in.Item["id"].(*types.AttributeValueMemberS)
	if !ok {
		return nil, errors.New("the item has no string id")
	}
	f.rows[key.Value] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}

func TestDynamoItemStorePutsAndGets(t *testing.T) {
	fake := &fakeDynamo{rows: map[string]map[string]types.AttributeValue{}}
	store := NewDynamoItemStore(fake, "items")
	want := Item{ID: "lamp", Name: "Lamp", Quantity: 3}
	if err := store.Put(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if fake.table != "items" {
		t.Errorf("Put wrote to table %q, want items", fake.table)
	}
	got, found, err := store.Get(t.Context(), "lamp")
	if err != nil || !found || got != want {
		t.Errorf("Get(lamp) = %+v, %v, %v; want %+v", got, found, err, want)
	}
	if _, found, err := store.Get(t.Context(), "chair"); err != nil || found {
		t.Errorf("Get(chair) found = %v, err = %v; want none", found, err)
	}
}

func TestDynamoItemStoreRefusesAMalformedRow(t *testing.T) {
	for name, row := range map[string]map[string]types.AttributeValue{
		"no name":         {"quantity": &types.AttributeValueMemberN{Value: "1"}},
		"a numeric name":  {"name": &types.AttributeValueMemberN{Value: "1"}, "quantity": &types.AttributeValueMemberN{Value: "1"}},
		"no quantity":     {"name": &types.AttributeValueMemberS{Value: "Lamp"}},
		"a fraction":      {"name": &types.AttributeValueMemberS{Value: "Lamp"}, "quantity": &types.AttributeValueMemberN{Value: "1.5"}},
		"a negative":      {"name": &types.AttributeValueMemberS{Value: "Lamp"}, "quantity": &types.AttributeValueMemberN{Value: "-1"}},
		"a string number": {"name": &types.AttributeValueMemberS{Value: "Lamp"}, "quantity": &types.AttributeValueMemberS{Value: "1"}},
	} {
		fake := &fakeDynamo{rows: map[string]map[string]types.AttributeValue{"lamp": row}}
		if _, _, err := NewDynamoItemStore(fake, "items").Get(t.Context(), "lamp"); err == nil {
			t.Errorf("Get of a row with %s succeeds", name)
		}
	}
}

func TestDynamoItemStoreWrapsTheClientsError(t *testing.T) {
	outage := errors.New("timeout")
	store := NewDynamoItemStore(&fakeDynamo{err: outage}, "items")
	if _, _, err := store.Get(t.Context(), "lamp"); !errors.Is(err, outage) {
		t.Errorf("Get error = %v, want it to wrap %v", err, outage)
	}
	if err := store.Put(t.Context(), Item{ID: "lamp", Name: "Lamp"}); !errors.Is(err, outage) {
		t.Errorf("Put error = %v, want it to wrap %v", err, outage)
	}
}
