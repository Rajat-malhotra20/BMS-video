"""Looks adapters up by name. Adding a vendor: write vendors/<name>_adapter.py,
register it in main.py. Ported from prototype/backend/vendors/adapter.go.
"""


class Registry:
    def __init__(self, *adapters):
        self._adapters = {a.name(): a for a in adapters}

    def get(self, vendor: str):
        adapter = self._adapters.get(vendor)
        if adapter is None:
            raise KeyError(f'unknown vendor "{vendor}"')
        return adapter

    def all(self) -> list:
        return list(self._adapters.values())
