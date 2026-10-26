#!/usr/bin/env python3
"""Run every interface test and write a self-contained HTML report."""

from __future__ import annotations

import os
import sys
from datetime import datetime

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

from lib.config import ConfigError, load_config  # noqa: E402


def main() -> int:
    try:
        load_config()
    except ConfigError as exc:
        print(f"配置错误：{exc}", file=sys.stderr)
        return 2

    import pytest

    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    report_dir = os.path.join(HERE, "reports")
    os.makedirs(report_dir, exist_ok=True)
    report = os.path.join(report_dir, f"report-{stamp}.html")

    code = int(
        pytest.main(
            [
                os.path.join(HERE, "cases"),
                "-q",
                "-c",
                os.path.join(HERE, "pytest.ini"),
                f"--html={report}",
                "--self-contained-html",
            ]
        )
    )
    print(f"报告：{report}")
    print(f"退出码：{code}")
    return code


if __name__ == "__main__":
    sys.exit(main())
