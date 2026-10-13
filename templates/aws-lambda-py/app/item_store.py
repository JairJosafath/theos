"""Items in DynamoDB, behind the ItemStore protocol the handler's logic uses."""

from typing import TYPE_CHECKING, Protocol, Unpack

from app.items import InvalidInputError, Item, check_fields

if TYPE_CHECKING:
    from types_boto3_dynamodb.type_defs import (
        GetItemInputTableGetItemTypeDef,
        GetItemOutputTableTypeDef,
        PutItemInputTablePutItemTypeDef,
        PutItemOutputTableTypeDef,
    )


class ItemStore(Protocol):
    """Where items live. The handler's logic depends on this, never on DynamoDB."""

    def get(self, item_id: str) -> Item | None:
        """Return the item with this id, or None when there is none."""
        ...

    def put(self, item: Item) -> None:
        """Store the item, replacing one with the same id."""
        ...


class Table(Protocol):
    """The two methods of boto3's DynamoDB Table the store calls.

    A test passes a fake instead, without the network.
    """

    def get_item(
        self, **kwargs: Unpack[GetItemInputTableGetItemTypeDef]
    ) -> GetItemOutputTableTypeDef:
        """Read one item by its key."""
        ...

    def put_item(
        self, **kwargs: Unpack[PutItemInputTablePutItemTypeDef]
    ) -> PutItemOutputTableTypeDef:
        """Write one whole item."""
        ...


class MalformedItemError(Exception):
    """An item in the table that isn't one the API wrote."""


class DynamoDbItemStore:
    """Items in a DynamoDB table whose partition key is the string `id`."""

    def __init__(self, table: Table) -> None:
        """Keep items in `table`."""
        self._table = table

    def get(self, item_id: str) -> Item | None:
        """Return the item with this id, or None when there is none."""
        output = self._table.get_item(Key={"id": item_id})
        if "Item" not in output:
            return None
        try:
            return check_fields(item_id, output["Item"])
        except InvalidInputError as error:
            msg = f"Item {item_id} is malformed: {error}"
            raise MalformedItemError(msg) from error

    def put(self, item: Item) -> None:
        """Store the item, replacing one with the same id."""
        self._table.put_item(Item={"id": item.id, "name": item.name, "quantity": item.quantity})
