"""A sequence of steps with one owner."""


def summarize(rows, scale):
    """Each row scaled, its total and its largest entry, and the rows past the mean total."""
    totals, largest = [], []
    for row in rows:
        scaled = [value * scale for value in row]
        totals.append(sum(scaled))
        largest.append(max(scaled) if scaled else 0.0)
    limit = sum(totals) / max(len(totals), 1)
    past = [index for index, total in enumerate(totals) if total > limit]
    return {"totals": totals, "largest": largest, "past": past}
