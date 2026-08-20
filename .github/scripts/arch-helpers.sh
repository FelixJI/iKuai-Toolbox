#!/usr/bin/env bash

# Why: keep artifact names stable across jobs and platforms / 为什么：统一不同平台产物命名，避免 workflow 到处散落字符串
# Why/为什么: 矩阵以 label 为唯一命名键（与 goos/goarch 构建参数解耦）；资产后缀在此
# 集中映射，保证与 Rust 时代的资产名逐字节一致（含 linux-riscv64 -> riscv64gc 等历史命名）。
# English: matrix entries are keyed by label (decoupled from goos/goarch build
# inputs); asset suffixes are mapped here so names stay byte-identical to the
# Rust era (including legacy quirks such as linux-riscv64 -> riscv64gc).

ikb_release_suffix() {
  local label="${1:-}"

  case "${label}" in
    linux-amd64)
      printf '%s\n' "linux-x86_64"
      ;;
    linux-386)
      printf '%s\n' "linux-x86_32"
      ;;
    linux-arm5|linux-arm6|linux-arm7)
      printf '%s\n' "${label}"
      ;;
    linux-arm64)
      printf '%s\n' "linux-aarch64"
      ;;
    linux-ppc64le)
      printf '%s\n' "linux-ppc64le"
      ;;
    linux-riscv64)
      # Why/为什么: 沿用 Rust 时代 riscv64gc 的历史资产名，保证升级用户下载链接不漂移。
      # English: keep the Rust-era riscv64gc asset name so upgrade download links stay stable.
      printf '%s\n' "linux-riscv64gc"
      ;;
    linux-mips)
      printf '%s\n' "linux-mips"
      ;;
    linux-mipsel)
      printf '%s\n' "linux-mipsle"
      ;;
    linux-mips64)
      printf '%s\n' "linux-mips64"
      ;;
    linux-mips64el)
      printf '%s\n' "linux-mips64le"
      ;;
    windows-amd64)
      printf '%s\n' "windows-x86_64"
      ;;
    macos-amd64)
      printf '%s\n' "macos-x86_64"
      ;;
    macos-arm64)
      printf '%s\n' "macos-aarch64"
      ;;
    freebsd-amd64)
      printf '%s\n' "freebsd-x86_64"
      ;;
    freebsd-386)
      printf '%s\n' "freebsd-x86_32"
      ;;
    *)
      # Why/为什么: GUI 标签（linux-x86_64 / android-armv7 / ios-aarch64 等）本身就是最终后缀。
      # English: GUI labels (linux-x86_64 / android-armv7 / ios-aarch64 ...) already are final suffixes.
      printf '%s\n' "${label}"
      ;;
  esac
}

ikb_release_os() {
  local suffix
  suffix="$(ikb_release_suffix "${1:-}")"
  printf '%s\n' "${suffix%%-*}"
}

ikb_cli_zip_name() {
  printf '%s\n' "ikuai-bypass-cli-$(ikb_release_suffix "${1:-}").zip"
}

ikb_gui_zip_name() {
  local suffix
  suffix="$(ikb_release_suffix "${1:-}")"
  if [[ "$(ikb_release_os "${1:-}")" == "windows" ]]; then
    printf '%s\n' "ikuai-bypass-gui-${suffix}.exe.zip"
    return
  fi
  printf '%s\n' "ikuai-bypass-gui-${suffix}.zip"
}

ikb_cli_native_name() {
  local ext="${2:-}"
  printf '%s\n' "ikuai-bypass-cli-$(ikb_release_suffix "${1:-}")${ext}"
}

ikb_gui_native_name() {
  local ext="${2:-}"
  printf '%s\n' "ikuai-bypass-gui-$(ikb_release_suffix "${1:-}")${ext}"
}

ikb_luci_base() {
  printf '%s\n' "ikuai-bypass-luci-openwrt-all"
}

ikb_ipkg_name() {
  local suffix
  suffix="$(ikb_release_suffix "${1:-}")"
  # Why/为什么: ipkg 只在 linux-amd64 / linux-arm64 上调用，去掉 linux- 前缀即架构名。
  # English: ipkg is only built for linux-amd64 / linux-arm64; stripping the
  # linux- prefix yields the bare arch used by the ipkg asset name.
  printf '%s\n' "ikuai-bypass-${suffix#linux-}.ipkg"
}
