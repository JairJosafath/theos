from decimal import Decimal

import pytest

from app.items import (
    InvalidInputError,
    Item,
    check_fields,
    check_id,
    check_item,
    json_response,
    problem,
)


@pytest.mark.parametrize("raw", ["a", "A-z_09", "x" * 64], ids=["one", "alphabet", "longest"])
def test_check_id_takes_url_safe_ids(raw: str) -> None:
    assert check_id(raw) == raw


@pytest.mark.parametrize(
    "raw",
    [None, "", "a b", "a/b", "é", "x" * 65, "a\n"],
    ids=["none", "empty", "space", "slash", "non-ascii", "too long", "trailing newline"],
)
def test_check_id_refuses_other_ids(raw: str | None) -> None:
    with pytest.raises(InvalidInputError, match="The id must be"):
        check_id(raw)


def test_check_item_reads_a_json_body_and_trims_the_name() -> None:
    item = check_item("i1", '{"name": " Lamp ", "quantity": 3}')

    assert item == Item(id="i1", name="Lamp", quantity=3)


@pytest.mark.parametrize(
    ("body", "error"),
    [
        (None, "must be JSON"),
        ("{", "must be JSON"),
        ("[1]", "must be a JSON object"),
        ("null", "must be a JSON object"),
        ('{"quantity": 1}', '"name"'),
        ('{"name": "  ", "quantity": 1}', '"name"'),
        ('{"name": 3, "quantity": 1}', '"name"'),
        ('{"name": "' + "x" * 201 + '", "quantity": 1}', '"name"'),
        ('{"name": "Lamp"}', '"quantity"'),
        ('{"name": "Lamp", "quantity": -1}', '"quantity"'),
        ('{"name": "Lamp", "quantity": 1.5}', '"quantity"'),
        ('{"name": "Lamp", "quantity": "1"}', '"quantity"'),
        ('{"name": "Lamp", "quantity": true}', '"quantity"'),
        ('{"name": "Lamp", "quantity": 9007199254740992}', '"quantity"'),
        ('{"name": "Lamp", "quantity": 1' + "0" * 5000 + "}", "must be JSON"),
    ],
    ids=[
        "no body",
        "not json",
        "an array",
        "null",
        "no name",
        "blank name",
        "name not a string",
        "name too long",
        "no quantity",
        "negative quantity",
        "fractional quantity",
        "quantity a string",
        "quantity a boolean",
        "quantity too large",
        "number too long to read",
    ],
)
def test_check_item_refuses_bad_bodies(body: str | None, error: str) -> None:
    with pytest.raises(InvalidInputError, match=error):
        check_item("i1", body)


@pytest.mark.parametrize(
    ("quantity", "want"),
    [(Decimal(3), 3), (Decimal("4.0"), 4), (0, 0), (2**53 - 1, 2**53 - 1)],
    ids=["decimal", "whole decimal", "zero", "largest"],
)
def test_check_fields_takes_dynamodb_numbers(quantity: object, want: int) -> None:
    assert check_fields("i1", {"id": "i1", "name": "Lamp", "quantity": quantity}).quantity == want


@pytest.mark.parametrize(
    "quantity",
    [Decimal("1.5"), Decimal("NaN"), Decimal("Infinity")],
    ids=["fraction", "nan", "infinity"],
)
def test_check_fields_refuses_other_decimals(quantity: Decimal) -> None:
    with pytest.raises(InvalidInputError, match='"quantity"'):
        check_fields("i1", {"name": "Lamp", "quantity": quantity})


def test_json_response_writes_an_item_as_its_fields() -> None:
    response = json_response(200, Item(id="i1", name="Zoë", quantity=1))

    assert response == {
        "statusCode": 200,
        "headers": {"content-type": "application/json"},
        "body": '{"id":"i1","name":"Zoë","quantity":1}',
    }


def test_problem_answers_with_one_message() -> None:
    assert problem(404, "No item i1.")["body"] == '{"message":"No item i1."}'
