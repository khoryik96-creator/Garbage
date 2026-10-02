class NotFound(LookupError):
    pass


class Conflict(ValueError):
    pass


class LostLease(RuntimeError):
    pass
