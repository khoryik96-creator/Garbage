"""Read-only observations of Kano's browser-SPA schema, not public API write payloads."""

from collections.abc import Mapping
from typing import Literal

from jobadder_autocoder.contracts.models import Contract, is_empty
from jobadder_autocoder.policy.country import normalize_country


class CountryObservation(Contract):
    presence: Literal["empty", "existing", "unavailable"]
    normalized: str | None = None
    warning: str | None = None

    @property
    def is_missing(self) -> bool:
        return self.presence == "empty"


def observe_kano_spa_country(snapshot: Mapping[str, object]) -> CountryObservation:
    """Fail closed for partial records; either populated address component blocks a fill."""
    address = snapshot.get("address")
    if not isinstance(address, Mapping):
        return CountryObservation(
            presence="unavailable", warning="Address fields were not available."
        )
    paths = ("country", "countryCode")
    values = [address[key] for key in paths if key in address]
    if any(value is not None and not isinstance(value, str) for value in values):
        return CountryObservation(presence="unavailable", warning="Unexpected Country field shape.")
    nonempty = [value for value in values if isinstance(value, str) and not is_empty(value)]
    if nonempty:
        codes = [normalize_country(value) for value in nonempty]
        if None in codes or len(set(codes)) != 1:
            return CountryObservation(
                presence="existing", warning="Existing Country values conflict or are unrecognized."
            )
        return CountryObservation(presence="existing", normalized=codes[0])
    if all(key in address for key in paths):
        return CountryObservation(presence="empty")
    return CountryObservation(
        presence="unavailable", warning="The record omitted a Country component."
    )
