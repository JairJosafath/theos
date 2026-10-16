"""The Lambda entry point the stack names: wiring only, the work is in route."""

import os
from http import HTTPStatus
from typing import TYPE_CHECKING

import boto3
from aws_lambda_powertools import Logger, Metrics
from aws_lambda_powertools.metrics import MetricUnit
from aws_lambda_powertools.utilities.idempotency import DynamoDBPersistenceLayer

from app.api import PUT_ITEM, HttpApiEvent, answer, route
from app.item_store import DynamoDbItemStore
from app.once import idempotency_config, once_per_key

if TYPE_CHECKING:
    from collections.abc import Mapping

    from aws_lambda_powertools.utilities.typing import LambdaContext

    from app.items import HttpResponse


def _setting(name: str) -> str:
    """Return the value of an environment variable the stack sets."""
    value = os.environ.get(name, "")
    if not value:
        msg = f"{name} is not set; the stack sets it."
        raise RuntimeError(msg)
    return value


# Created once per execution environment, outside the handler, so warm
# invocations reuse the clients and their connections. Powertools reads the
# service's name and the metrics' namespace from the variables the stack sets.
_STORE = DynamoDbItemStore(boto3.resource("dynamodb").Table(_setting("TABLE_NAME")))
_LOGGER = Logger()
_METRICS = Metrics()

# A PUT sent again with the same Idempotency-Key header gets the first answer
# back and writes nothing twice: app/once.py says how.
_IDEMPOTENCY = idempotency_config()
_PUT_ONCE = once_per_key(
    lambda event: route(event, _STORE),
    DynamoDBPersistenceLayer(table_name=_setting("IDEMPOTENCY_TABLE_NAME")),
    _IDEMPOTENCY,
)


def _log(message: str, attributes: Mapping[str, object]) -> None:
    _LOGGER.error(message, extra=attributes)


@_METRICS.log_metrics(capture_cold_start_metric=True)
@_LOGGER.inject_lambda_context
def handler(event: HttpApiEvent, context: LambdaContext) -> HttpResponse:
    """Answer one API Gateway request, counting answers and failures."""
    _IDEMPOTENCY.register_lambda_context(context)
    response = answer(
        event,
        _PUT_ONCE if event["routeKey"] == PUT_ITEM else lambda request: route(request, _STORE),
        _log,
    )
    failed = response["statusCode"] >= HTTPStatus.INTERNAL_SERVER_ERROR
    _METRICS.add_metric(
        name="FailedRequests" if failed else "AnsweredRequests", unit=MetricUnit.Count, value=1
    )
    return response
