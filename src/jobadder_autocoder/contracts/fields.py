from functools import lru_cache
from importlib.resources import files
from typing import Literal, Self

from pydantic import Field, model_validator

from jobadder_autocoder.contracts.models import Contract


class ReferenceSource(Contract):
    repository: str
    commit: str = Field(pattern=r"^[a-f0-9]{40}$")


class FieldDefinition(Contract):
    key: str
    label: str
    kano_paths: tuple[str, ...] = Field(min_length=1)
    source_file: str
    account_specific: bool = False
    enabled_for_runs: bool = False
    public_v2_mapping_verified: bool = False
    note: str


class FieldCatalog(Contract):
    schema_version: Literal[1]
    source: ReferenceSource
    observed_transport: Literal["browser_spa"]
    fields: tuple[FieldDefinition, ...]

    @model_validator(mode="after")
    def validate_scope(self) -> Self:
        keys = [field.key for field in self.fields]
        if len(keys) != len(set(keys)):
            raise ValueError("Field keys must be unique.")
        if [field.key for field in self.fields if field.enabled_for_runs] != ["country"]:
            raise ValueError("The current prototype supports Country only.")
        for field in self.fields:
            if any(path.startswith("customFields.") for path in field.kano_paths):
                if not field.account_specific:
                    raise ValueError("Numeric custom-field mappings must be account-specific.")
        return self

    def field(self, key: str) -> FieldDefinition:
        for field in self.fields:
            if field.key == key:
                return field
        raise KeyError(key)


@lru_cache(maxsize=1)
def field_catalog() -> FieldCatalog:
    resource = files("jobadder_autocoder.contracts").joinpath("field_catalog.json")
    return FieldCatalog.model_validate_json(resource.read_text(encoding="utf-8"))
