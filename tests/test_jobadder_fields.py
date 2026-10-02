from copy import deepcopy

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from jobadder_autocoder.connectors.jobadder_fields import observe_kano_spa_country
from jobadder_autocoder.contracts.fields import FieldCatalog, field_catalog


@pytest.mark.parametrize(
    ("address", "normalized"),
    [
        ({"country": "Malaysia", "countryCode": "MY"}, "MY"),
        ({"country": "Malaysia", "countryCode": ""}, "MY"),
        ({"country": "Malaysia", "countryCode": None}, "MY"),
        ({"country": "Malaysia"}, "MY"),
        ({"country": "", "countryCode": "MY"}, "MY"),
        ({"countryCode": "MY"}, "MY"),
        ({"country": "Malaysia", "countryCode": "SG"}, None),
        ({"country": "Unknown", "countryCode": ""}, None),
        ({"country": "N/A", "countryCode": None}, None),
    ],
)
def test_either_existing_country_component_blocks_fill(
    address: dict[str, object], normalized: str | None
) -> None:
    snapshot: dict[str, object] = {"address": address, "email": "existing@example.test"}
    before = deepcopy(snapshot)
    observation = observe_kano_spa_country(snapshot)
    assert observation.presence == "existing"
    assert observation.normalized == normalized
    assert not observation.is_missing
    assert snapshot == before


@pytest.mark.parametrize(
    "address",
    [
        {"country": None, "countryCode": None},
        {"country": "", "countryCode": ""},
        {"country": "  ", "countryCode": None},
    ],
)
def test_only_two_explicit_empty_components_allow_a_fill(address: dict[str, object]) -> None:
    observation = observe_kano_spa_country({"address": address})
    assert observation.presence == "empty"
    assert observation.is_missing


@pytest.mark.parametrize(
    "snapshot",
    [
        {},
        {"address": None},
        {"address": []},
        {"address": {}},
        {"address": {"country": ""}},
        {"address": {"countryCode": None}},
        {"address": {"country": [], "countryCode": ""}},
        {"address": {"country": "Malaysia", "countryCode": {"value": "MY"}}},
        {"address": {"country": None, "countryCode": 0}},
        {"address": {"state": "Selangor"}, "phone": "+60123456789"},
    ],
)
def test_partial_or_malformed_country_records_block_fill(snapshot: dict[str, object]) -> None:
    observation = observe_kano_spa_country(snapshot)
    assert observation.presence == "unavailable"
    assert not observation.is_missing
    assert observation.warning


def test_catalog_labels_transport_and_account_specific_custom_fields() -> None:
    catalog = field_catalog()
    assert catalog.source.commit == "58c857dad907cf73c0e3281ba449e142ce81b9c9"
    assert catalog.observed_transport == "browser_spa"
    assert [field.key for field in catalog.fields if field.enabled_for_runs] == ["country"]
    assert not any(field.public_v2_mapping_verified for field in catalog.fields)
    assert catalog.field("country").kano_paths == ("address.country", "address.countryCode")
    assert catalog.field("industry").account_specific
    assert catalog.field("industry_subcategory").account_specific
    with pytest.raises(KeyError):
        catalog.field("unmapped")


def test_catalog_rejects_ambiguous_or_unsafe_scope_declarations() -> None:
    payload = field_catalog().model_dump(mode="json")
    duplicated = deepcopy(payload)
    duplicated["fields"].append(deepcopy(duplicated["fields"][0]))
    with pytest.raises(ValidationError, match="Field keys must be unique"):
        FieldCatalog.model_validate(duplicated)
    unscoped = deepcopy(payload)
    for field in unscoped["fields"]:
        if field["key"] == "industry":
            field["account_specific"] = False
    with pytest.raises(ValidationError, match="account-specific"):
        FieldCatalog.model_validate(unscoped)
    widened = deepcopy(payload)
    for field in widened["fields"]:
        if field["key"] == "email":
            field["enabled_for_runs"] = True
    with pytest.raises(ValidationError, match="Country only"):
        FieldCatalog.model_validate(widened)


def test_api_catalog_does_not_enable_future_run_fields(client: TestClient) -> None:
    response = client.get("/api/fields")
    assert response.status_code == 200
    catalog = FieldCatalog.model_validate(response.json())
    assert len(catalog.fields) == 20
    for field in catalog.fields:
        if not field.enabled_for_runs:
            rejected = client.post("/api/runs", json={"fields": [field.key]})
            assert rejected.status_code == 422
