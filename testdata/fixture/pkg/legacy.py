"""An older near copy of summarize, which predates any diff, so no diff is tapped for it."""


def summarize_before(rows, scale):
    totals, largest = [], []
    for row in rows:
        scaled = [value * scale for value in row]
        totals.append(sum(scaled))
        largest.append(max(scaled) if scaled else 0.0)
    limit = sum(totals) / max(len(totals), 1)
    past = [index for index, total in enumerate(totals) if total >= limit]
    return {"totals": totals, "largest": largest, "past": past}
