import datetime as dt
from zoneinfo import ZoneInfo

from slate.services.repo_board import closes_by_week
from slate.services.repo_board import week_starts


def test_a_close_counts_in_the_week_its_day_falls_in_where_the_board_is_read():
    """Half past eleven on a Sunday night in New York is already Monday in UTC, so it closes the week before."""
    new_york = ZoneInfo('America/New_York')
    weeks = week_starts(dt.date(2026, 11, 7), 3)
    assert weeks == [dt.date(2026, 10, 19), dt.date(2026, 10, 26), dt.date(2026, 11, 2)]
    sunday_night = dt.datetime(2026, 11, 1, 23, 30, tzinfo=new_york)
    monday_morning = dt.datetime(2026, 11, 2, 9, tzinfo=new_york)
    before_the_first = dt.datetime(2026, 10, 18, 12, tzinfo=new_york)
    assert closes_by_week([sunday_night, monday_morning, before_the_first], weeks, new_york) == [0, 1, 1]
