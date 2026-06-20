{
  description = "scraper-pedco (Go + sqlite3 via cgo) - devShell + package for NixOS";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go gopls gotools go-outline delve golangci-lint
            gcc pkg-config
            sqlite
            openssl
          ];

          shellHook = ''
            export CGO_ENABLED=1
            echo "scraper-pedco devShell listo (CGO_ENABLED=1)"
          '';
        };

        packages.pedco-bot = pkgs.buildGoModule {
          pname = "pedco-bot";
          version = "0.1.0";
          src = ./.;

          subPackages = [ "cmd/scraper" ];

          nativeBuildInputs = with pkgs; [ pkg-config ];
          buildInputs = with pkgs; [ sqlite ];

          env = { CGO_ENABLED = "1"; };

          vendorHash = "sha256-ZuuuMAsj+dM/Vu4dTz6+v9AXcgA7XVAwmaSAjz0qsIQ=";

          postInstall = ''
            if [ -f "$out/bin/scraper" ]; then
              mv "$out/bin/scraper" "$out/bin/pedco-bot"
            fi
          '';
        };

        packages.default = self.packages.${system}.pedco-bot;

        apps.default = {
          type = "app";
          program = "${self.packages.${system}.pedco-bot}/bin/pedco-bot";
        };
      });
}
