from decimal import Decimal
from typing import TYPE_CHECKING, Unpack, cast

import pytest

from app.item_store import DynamoDbItemStore, MalformedItemError
from app.items import Item

if TYPE_CHECKING:
    from types_boto3_dynamodb.type_defs import (
        GetItemInputTableGetItemTypeDef,
        GetItemOutputTableTypeDef,
        PutItemInputTablePutItemTypeDef,
        PutItemOutputTableTypeDef,
        ResponseMetadataTypeDef,
    )


class FakeTable:
    """A boto3 Table that keeps rows in memory and records each call."""

    def __init__(self, rows: dict[str, dict[str, object]] | None = None) -> None:
        self.rows = rows or {}
        self.calls: list[tuple[str, object]] = []

    def get_item(
        self, **kwargs: Unpack[GetItemInputTableGetItemTypeDef]
    ) -> GetItemOutputTableTypeDef:
        self.calls.append(("get_item", kwargs))
        key = kwargs["Key"]["id"]
        output: dict[str, object] = {"ResponseMetadata": _metadata()}
        if isinstance(key, str) and key in self.rows:
            output["Item"] = self.rows[key]
        return cast("GetItemOutputTableTypeDef", output)

    def put_item(
        self, **kwargs: Unpack[PutItemInputTablePutItemTypeDef]
    ) -> PutItemOutputTableTypeDef:
        self.calls.append(("put_item", kwargs))
        return cast("PutItemOutputTableTypeDef", {"ResponseMetadata": _metadata()})


def _metadata() -> ResponseMetadataTypeDef:
    return {
        "RequestId": "r",
        "HTTPStatusCode": 200,
        "HTTPHeaders": {},
        "RetryAttempts": 0,
    }


def test_get_reads_the_item_by_its_key() -> None:
    table = FakeTable({"i1": {"id": "i1", "name": "Lamp", "quantity": Decimal(3)}})

    item = DynamoDbItemStore(table).get("i1")

    assert item == Item(id="i1", name="Lamp", quantity=3)
    assert table.calls == [("get_item", {"Key": {"id": "i1"}})]


def test_get_returns_none_for_a_missing_item() -> None:
    assert DynamoDbItemStore(FakeTable()).get("nope") is None


def test_get_refuses_a_malformed_row() -> None:
    table = FakeTable({"i1": {"id": "i1", "name": "Lamp"}})

    with pytest.raises(MalformedItemError, match='Item i1 is malformed: "quantity"'):
        DynamoDbItemStore(table).get("i1")


def test_put_writes_the_whole_item() -> None:
    table = FakeTable()

    DynamoDbItemStore(table).put(Item(id="i1", name="Lamp", quantity=3))

    assert table.calls == [("put_item", {"Item": {"id": "i1", "name": "Lamp", "quantity": 3}})]
