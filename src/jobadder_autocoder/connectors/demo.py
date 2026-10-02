from jobadder_autocoder.contracts.models import Candidate, CandidatePage, Mutation
from jobadder_autocoder.policy.country import PolicyError, normalize_country
from jobadder_autocoder.storage.repository import Repository


class DemoConnector:
    """Atomic local adapter. This is not a JobAdder client."""

    def __init__(self, repository: Repository):
        self.repository = repository

    def page(self, *, after: int, through: int, limit: int) -> CandidatePage:
        return self.repository.page_candidates(after, through, limit)

    def get(self, candidate_id: int) -> Candidate:
        return self.repository.get_candidate(candidate_id)

    def fill_country(self, candidate: Candidate, value: str) -> Mutation:
        if normalize_country(value) != value:
            raise PolicyError("Use a valid ISO country code.")
        return self.repository.mutate_country(candidate, value)

    def clear_country(
        self, candidate: Candidate, expected_value: str, restore_value: str | None
    ) -> Mutation:
        return self.repository.mutate_country(
            candidate, restore_value, expected_value=expected_value
        )


def seed_demo(repository: Repository) -> None:
    if repository.candidate_counts()["total"]:
        return
    profiles = [
        (1001, "Alex Morgan", None, "Australia", ""),
        (1002, "Jordan Lee", "NZ", "New Zealand", ""),
        (1003, "Sam Taylor", "", None, "Country of residence: New Zealand"),
        (1004, "Casey Reed", None, None, "Based in: United Kingdom"),
        (1005, "Avery Quinn", None, None, "Previously worked in Australia. Citizenship: Canadian."),
        (1006, "Riley Park", None, "Australia", "Country of residence: New Zealand"),
        (1007, "Drew Ellis", "Unknown", "Singapore", ""),
        (1008, "Jamie Blake", None, "Singapore", ""),
        (1009, "Cameron Gray", None, None, "No residence information supplied."),
        (1010, "Robin Lane", None, "Atlantis", ""),
    ]
    for candidate_id, name, country, address, notes in profiles:
        repository.add_candidate(
            Candidate(
                id=candidate_id,
                name=f"Demo {name}",
                country=country,
                address_country=address,
                notes=notes,
                other_fields={
                    "email": f"demo{candidate_id}@example.invalid",
                    "title": "Demo analyst",
                },
            )
        )
    repository.audit("demo_seeded", details={"profiles": len(profiles)})
