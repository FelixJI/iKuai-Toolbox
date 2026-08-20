{
  description = "Go CLI / Tauri Linux x86 GUI / Bun Astro 前端 / Jekyll 文档 开发编译环境";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  };

  outputs =
    { nixpkgs, ... }:
    let
      env = import ./flake_pkgs_let.nix { inherit nixpkgs; };
      inherit (env)
        system
        pkgs
        basePackages
        golangPackages
        rustPackages
        desktopPackages
        android
        jsPackages
        docsPackages
        playwrightPackages
        playwrightLibPath
        ;

      bootstrapReleaseTools = pkgs.writeShellScriptBin "ikb-bootstrap-release-tools" ''
        set -euo pipefail
        cargo binstall -y tauri-cli
      '';
    in
    {
      devShells.${system}.default = pkgs.mkShell {
        packages =
          basePackages.all
          ++ golangPackages.packages
          ++ rustPackages.packages
          ++ desktopPackages.packages
          ++ android.packages
          ++ jsPackages
          ++ docsPackages.jekyll
          ++ playwrightPackages
          ++ [ bootstrapReleaseTools ];

        env = golangPackages.env // rustPackages.env // desktopPackages.env // android.env // {
          LD_LIBRARY_PATH = "${playwrightLibPath}:${rustPackages.libPath}";
        };

        shellHook = ''
          export PATH="$HOME/.cargo/bin:/run/current-system/sw/bin:/etc/profiles/per-user/$USER/bin:$PATH"
          export SCCACHE_DIR="$HOME/.cache/sccache"

          echo "==========================================================="
          echo "== iKuai Bypass devShell =="
          echo "  Go 工具链：go / gopls / delve（CLI 主线）"
          echo "  Rust 工具链由 rustup 管理（仅 Tauri GUI），首次进入请执行："
          echo "    rustup default stable"
          echo "  bun 依赖："
          echo "    bun install"
          echo "  Jekyll 文档本地预览："
          echo "    bundle install && bundle exec jekyll serve"
          echo "==========================================================="
          if [ -t 0 ] && command -v fish >/dev/null 2>&1; then
            export __FISH_DEVSHELL=1
            exec fish
          fi
        '';
      };
    };
}
