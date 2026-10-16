"""A PUT sent again with the same Idempotency-Key header changes nothing twice.

Powertools' Idempotency keeps the first answer to each key and gives it back
to a request sent again with that key; one without the header is simply run.
Only what changed something is kept: a refusal or a failure keeps no record,
so a corrected retry runs again. The key sent with another path or body is
refused (422), and so is a retry while the first is still running (409).
"""

from http import HTTPStatus
from typing import TYPE_CHECKING

from aws_lambda_powertools.utilities.idempotency import IdempotencyConfig, idempotent_function
from aws_lambda_powertools.utilities.idempotency.exceptions import (
    IdempotencyAlreadyInProgressError,
    IdempotencyValidationError,
)

from app.items import problem

if TYPE_CHECKING:
    from collections.abc import Callable

    from aws_lambda_powertools.utilities.idempotency import BasePersistenceLayer

    from app.api import HttpApiEvent, Route
    from app.items import HttpResponse


def idempotency_config() -> IdempotencyConfig:
    """Key a request by its Idempotency-Key header, checked against its path and body."""
    return IdempotencyConfig(
        event_key_jmespath='headers."idempotency-key"',
        payload_validation_jmespath="[rawPath, body]",
        raise_on_no_idempotency_key=False,
    )


def once_per_key(
    route: Route, persistence_store: BasePersistenceLayer, config: IdempotencyConfig
) -> Route:
    """Return `route`, run once per Idempotency-Key, with its records in `persistence_store`."""
    return answering_once(idempotent(keeping_only_success(route), persistence_store, config))


def idempotent(
    route: Route, persistence_store: BasePersistenceLayer, config: IdempotencyConfig
) -> Route:
    """Return `route` through Powertools' Idempotency, which reads the whole event."""

    def run(*, event: HttpApiEvent) -> HttpResponse:
        return route(event)

    # Powertools hashes the keyword argument it is told to, so run takes one.
    once: Callable[..., HttpResponse] = idempotent_function(
        run,
        data_keyword_argument="event",
        persistence_store=persistence_store,
        config=config,
    )
    return lambda event: once(event=event)


class RefusedError(Exception):
    """A refusal (4xx) raised out of the idempotent work.

    Powertools keeps no record of it, so a corrected request with the same key
    runs again.
    """

    def __init__(self, response: HttpResponse) -> None:
        """Carry `response` out, to be answered as it was."""
        super().__init__(f"Refused with {response['statusCode']}.")
        self.response = response


def keeping_only_success(route: Route) -> Route:
    """Return `route`, raising its refusals as RefusedError: only what changed something is kept."""

    def run(event: HttpApiEvent) -> HttpResponse:
        response = route(event)
        if HTTPStatus.BAD_REQUEST <= response["statusCode"] < HTTPStatus.INTERNAL_SERVER_ERROR:
            raise RefusedError(response)
        return response

    return run


def answering_once(once: Route) -> Route:
    """Return `once`, the idempotent work, answering what it raises that a client can act on.

    A refusal is answered as it was, a request still running with the same key
    with 409, and the key sent with another request with 422. Anything else is
    raised on, for `answer` to log and answer with 500.
    """

    def run(event: HttpApiEvent) -> HttpResponse:
        try:
            return once(event)
        except RefusedError as refused:
            return refused.response
        except IdempotencyAlreadyInProgressError:
            return problem(409, "The same request is still running. Try again shortly.")
        except IdempotencyValidationError:
            return problem(422, "This Idempotency-Key was sent with another request.")

    return run
