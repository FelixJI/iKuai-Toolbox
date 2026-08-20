#!/usr/bin/env python3
"""Release PR automation for the Go mainline (vibetable-style Release PR flow).

Why/为什么: 版本号分散在 CoreVersion / Cargo.toml / Cargo.lock 三处，历史上靠手工
同步且无一致性校验；本脚本把它们收敛为单一自动流程：算出下一版本、同步改写三处、
写 .release/plan.json 并维护 release/auto 分支的 Release PR。
English: version strings live in three places with no consistency check; this
script computes the next version, rewrites all of them, records
.release/plan.json and maintains the release/auto Release PR.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

TAG_PREFIX = "ikuai-bypass-v"
RELEASE_BRANCH = "release/auto"
CARGO_PACKAGE_NAME = "ikb-app"
CORE_VERSION_PATH = Path("internal/app/diagnostics.go")
CARGO_TOML_PATH = Path("apps/gui/Cargo.toml")
CARGO_LOCK_PATH = Path("apps/gui/Cargo.lock")
PLAN_PATH = Path(".release/plan.json")

SEMVER_RE = re.compile(r"^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z][0-9A-Za-z.-]*))?$")
CORE_VERSION_RE = re.compile(r'^const CoreVersion = "([^"]+)"$', re.MULTILINE)


class PrepareError(RuntimeError):
    pass


def run(args: list[str], *, check: bool = True) -> subprocess.CompletedProcess:
    proc = subprocess.run(args, capture_output=True, text=True, encoding="utf-8")
    if check and proc.returncode != 0:
        raise PrepareError(
            f"command failed ({proc.returncode}): {' '.join(args)}\n"
            f"stdout: {proc.stdout}\nstderr: {proc.stderr}"
        )
    return proc


def parse_semver(value: str) -> tuple[tuple[int, int, int], str | None]:
    match = SEMVER_RE.match(value.strip())
    if match is None:
        raise PrepareError(f"invalid semver: {value!r}")
    base = (int(match.group(1)), int(match.group(2)), int(match.group(3)))
    return base, match.group(4)


def semver_sort_key(value: str) -> tuple:
    base, suffix = parse_semver(value)
    # Why/为什么: 无后缀的正式版排序高于任何预发布后缀（semver 语义）。
    # English: a bare release sorts above any prerelease suffix (semver rules).
    return base + (suffix is None, suffix or "")


def bump_base(base: tuple[int, int, int], part: str) -> tuple[int, int, int]:
    major, minor, patch = base
    if part == "major":
        return (major + 1, 0, 0)
    if part == "minor":
        return (major, minor + 1, 0)
    if part == "patch":
        return (major, minor, patch + 1)
    raise PrepareError(f"unsupported bump part: {part}")


def read_core_version() -> str:
    text = CORE_VERSION_PATH.read_text(encoding="utf-8")
    match = CORE_VERSION_RE.search(text)
    if match is None:
        raise PrepareError(f"CoreVersion constant not found in {CORE_VERSION_PATH}")
    return match.group(1)


def write_core_version(value: str) -> None:
    text = CORE_VERSION_PATH.read_text(encoding="utf-8")
    updated, count = CORE_VERSION_RE.subn(f'const CoreVersion = "{value}"', text)
    if count != 1:
        raise PrepareError(f"CoreVersion replacement failed in {CORE_VERSION_PATH}")
    CORE_VERSION_PATH.write_text(updated, encoding="utf-8", newline="\n")


def read_cargo_toml_version() -> str:
    text = CARGO_TOML_PATH.read_text(encoding="utf-8")
    in_package = False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("["):
            in_package = stripped == "[package]"
            continue
        if not in_package:
            continue
        match = re.match(r'^version\s*=\s*"([^"]+)"\s*$', stripped)
        if match:
            return match.group(1)
    raise PrepareError(f"package version not found in {CARGO_TOML_PATH}")


def write_cargo_toml_version(value: str) -> None:
    text = CARGO_TOML_PATH.read_text(encoding="utf-8")
    lines = text.splitlines(keepends=True)
    in_package = False
    replaced = 0
    for index, line in enumerate(lines):
        stripped = line.strip()
        if stripped.startswith("["):
            in_package = stripped == "[package]"
            continue
        if not in_package:
            continue
        if re.match(r'^version\s*=\s*"[^"]+"\s*$', stripped):
            lines[index] = f'version = "{value}"\n'
            replaced += 1
            break
    if replaced != 1:
        raise PrepareError(f"package version replacement failed in {CARGO_TOML_PATH}")
    CARGO_TOML_PATH.write_text("".join(lines), encoding="utf-8", newline="\n")


def read_cargo_lock_version() -> str:
    text = CARGO_LOCK_PATH.read_text(encoding="utf-8")
    pattern = re.compile(
        r'\[\[package\]\]\s*\nname = "'
        + re.escape(CARGO_PACKAGE_NAME)
        + r'"\s*\nversion = "([^"]+)"'
    )
    match = pattern.search(text)
    if match is None:
        raise PrepareError(
            f"{CARGO_PACKAGE_NAME} entry not found in {CARGO_LOCK_PATH}"
        )
    return match.group(1)


def write_cargo_lock_version(value: str) -> None:
    text = CARGO_LOCK_PATH.read_text(encoding="utf-8")
    pattern = re.compile(
        r'(\[\[package\]\]\s*\nname = "'
        + re.escape(CARGO_PACKAGE_NAME)
        + r'"\s*\nversion = )"[^"]+"'
    )
    updated, count = pattern.subn(rf'\g<1>"{value}"', text)
    if count != 1:
        raise PrepareError(
            f"{CARGO_PACKAGE_NAME} version replacement failed in {CARGO_LOCK_PATH}"
        )
    CARGO_LOCK_PATH.write_text(updated, encoding="utf-8", newline="\n")


def latest_release_tag_version() -> str | None:
    run(["git", "fetch", "origin", "main", "--tags"])
    proc = run(["git", "tag", "--list", f"{TAG_PREFIX}*"])
    versions = [line for line in proc.stdout.splitlines() if line.strip()]
    if not versions:
        return None
    best = max(versions, key=lambda tag: semver_sort_key(tag.removeprefix(TAG_PREFIX)))
    return best.removeprefix(TAG_PREFIX)


def existing_open_release_pr() -> dict | None:
    proc = run(
        [
            "gh",
            "pr",
            "list",
            "--state",
            "open",
            "--base",
            "main",
            "--head",
            RELEASE_BRANCH,
            "--json",
            "number,url,headRefName",
            "--limit",
            "5",
        ]
    )
    items = json.loads(proc.stdout or "[]")
    matching = [item for item in items if item.get("headRefName") == RELEASE_BRANCH]
    if not matching:
        return None
    if len(matching) > 1:
        raise PrepareError("multiple open release PRs use the canonical branch")
    return matching[0]


def release_pr_body(tag: str, base_tag: str | None, commits: str) -> str:
    lines = [
        "Automated release PR maintained by `.github/scripts/release-prepare.py`.",
        "",
        f"- release tag: `{tag}`",
    ]
    if base_tag:
        lines.append(f"- commit range: `{base_tag}..main`")
    lines += ["", "### Commits", "", commits]
    lines += [
        "",
        "Merge this PR to publish the release: `Release Tag` will tag the merge",
        "commit and forward it to the `Release go` workflow.",
    ]
    return "\n".join(lines)


def commit_range_log(base_tag: str | None) -> str:
    if base_tag:
        proc = run(
            [
                "git",
                "log",
                "--oneline",
                "--no-merges",
                "-n",
                "60",
                f"{base_tag}..origin/main",
            ],
            check=False,
        )
    else:
        proc = run(
            ["git", "log", "--oneline", "--no-merges", "-n", "60", "origin/main"],
            check=False,
        )
    return proc.stdout.strip() or "- (no commits)"


def verify_tag(tag: str) -> int:
    version = tag.removeprefix(TAG_PREFIX)
    base, _ = parse_semver(version)
    expected = ".".join(str(part) for part in base)
    for label, value in (
        ("CoreVersion", read_core_version()),
        ("Cargo.toml", read_cargo_toml_version()),
        ("Cargo.lock", read_cargo_lock_version()),
    ):
        value_base, _ = parse_semver(value)
        actual = ".".join(str(part) for part in value_base)
        if actual != expected:
            raise PrepareError(
                f"tag {tag} (base {expected}) disagrees with {label}={value} "
                f"(base {actual}); run the Release Prepare workflow to sync "
                "version sources before tagging"
            )
    print(f"version consistency OK for {tag}: {expected}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bump", choices=["patch", "minor", "major"])
    parser.add_argument(
        "--suffix",
        default="",
        help="prerelease suffix appended to the version, e.g. rc.1 (empty = stable)",
    )
    parser.add_argument(
        "--verify-tag",
        metavar="TAG",
        default=None,
        help="verify a canonical tag against the in-code version sources and exit",
    )
    args = parser.parse_args()

    if args.verify_tag:
        if args.bump:
            parser.error("--bump and --verify-tag are mutually exclusive")
        return verify_tag(args.verify_tag)
    if not args.bump:
        parser.error("--bump is required unless --verify-tag is given")

    suffix = args.suffix.strip().lstrip("-").strip()
    if suffix:
        parse_semver(f"1.0.0-{suffix}")

    core_version = read_core_version()
    cargo_version = read_cargo_toml_version()
    lock_version = read_cargo_lock_version()
    core_base, _ = parse_semver(core_version)
    for label, value in (("Cargo.toml", cargo_version), ("Cargo.lock", lock_version)):
        base, _ = parse_semver(value)
        if base != core_base:
            raise PrepareError(
                f"version sources disagree: CoreVersion={core_version} vs "
                f"{label}={value}"
            )

    tag_version = latest_release_tag_version()
    baseline_value = max(
        (core_version, tag_version or "0.0.0"),
        key=semver_sort_key,
    )
    baseline_base, _ = parse_semver(baseline_value)
    target_base = bump_base(baseline_base, args.bump)
    target = ".".join(str(part) for part in target_base) + (
        f"-{suffix}" if suffix else ""
    )
    tag = f"{TAG_PREFIX}{target}"

    if run(
        ["git", "rev-parse", "--verify", "--quiet", f"refs/tags/{tag}"],
        check=False,
    ).returncode == 0:
        raise PrepareError(f"tag {tag} already exists locally")

    existing_pr = existing_open_release_pr()
    if existing_pr:
        proc = run(
            [
                "git",
                "fetch",
                "origin",
                f"{RELEASE_BRANCH}:refs/remotes/origin/{RELEASE_BRANCH}",
            ],
            check=False,
        )
        if proc.returncode == 0:
            previous = json.loads(
                run(
                    ["git", "show", f"origin/{RELEASE_BRANCH}:{PLAN_PATH.as_posix()}"]
                ).stdout
                or "{}"
            )
            if previous.get("bump") != args.bump:
                raise PrepareError(
                    f"open release PR #{existing_pr['number']} requests bump="
                    f"{previous.get('bump')}; refusing {args.bump}. Merge or close "
                    "it first."
                )

    run(["git", "checkout", "-B", RELEASE_BRANCH, "origin/main"])
    write_core_version(target)
    write_cargo_toml_version(target)
    write_cargo_lock_version(target)
    for label, reader in (
        ("CoreVersion", read_core_version),
        ("Cargo.toml", read_cargo_toml_version),
        ("Cargo.lock", read_cargo_lock_version),
    ):
        if reader() != target:
            raise PrepareError(f"post-write verification failed for {label}")

    prepared_from_sha = run(["git", "rev-parse", "origin/main"]).stdout.strip()
    PLAN_PATH.parent.mkdir(parents=True, exist_ok=True)
    PLAN_PATH.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "bump": args.bump,
                "baseline_version": baseline_value,
                "version": target,
                "tag": tag,
                "prepared_from_sha": prepared_from_sha,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
        newline="\n",
    )

    run(["git", "add", str(CORE_VERSION_PATH), str(CARGO_TOML_PATH), str(CARGO_LOCK_PATH), str(PLAN_PATH)])
    run(["git", "config", "user.name", "github-actions[bot]"])
    run(
        [
            "git",
            "config",
            "user.email",
            "41898282+github-actions[bot]@users.noreply.github.com",
        ]
    )
    run(["git", "commit", "-m", f"chore(release): bump version to {tag}"])
    run(["git", "push", "--force-with-lease", "origin", RELEASE_BRANCH])

    # Why/为什么: PR body 的 commit 范围必须基于真实存在的上一个 tag，而不是可能
    # 只来自代码版本的 baseline。
    # English: the PR-body commit range must use the real previous tag, not a
    # baseline that may originate from the in-code version only.
    base_tag = f"{TAG_PREFIX}{tag_version}" if tag_version else None
    body = release_pr_body(tag, base_tag, commit_range_log(base_tag))
    if existing_pr:
        run(
            [
                "gh",
                "pr",
                "edit",
                str(existing_pr["number"]),
                "--title",
                f"release: {tag}",
                "--body",
                body,
            ]
        )
        print(f"Updated release PR #{existing_pr['number']} for {tag}")
    else:
        run(
            [
                "gh",
                "pr",
                "create",
                "--base",
                "main",
                "--head",
                RELEASE_BRANCH,
                "--title",
                f"release: {tag}",
                "--body",
                body,
            ]
        )
        print(f"Created release PR for {tag}")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except PrepareError as error:
        print(f"release-prepare: {error}", file=sys.stderr)
        sys.exit(1)
