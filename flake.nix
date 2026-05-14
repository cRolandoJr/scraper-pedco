{
    description = "Entorno de desarrollo para scraper de Pedco en Go";

    inputs = {
        nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    };

    outputs = { self, nixpkgs }:
    let
        system = "x86_64-linux";
        pkgs = nixpkgs.legacyPackages.${system};
    in
    {
        devShells.${system}.default = pkgs.mkShell {
            buildInputs = with pkgs; [
                # Toolchain Go
                go
                gopls
                gotools
                go-outline
                delve
                golangci-lint

                # CGO (mattn/go-sqlite3 requiere compilador C)
                gcc
                pkg-config

                # Utilidades runtime
                sqlite       # CLI para inspeccionar pedcobot.db
                openssl      # generar SECRET_KEY (openssl rand -base64 32)
            ];

            shellHook = ''
                export CGO_ENABLED=1
                echo "================================================="
                echo "Entorno Scraper Pedco listo."
                echo "Go: $(go version | awk '{print $3}')"
                echo "GCC: $(gcc --version | head -1 | awk '{print $NF}')"
                echo "SQLite: $(sqlite3 --version | awk '{print $1}')"
                echo "-------------------------------------------------"
                echo "Comandos útiles:"
                echo "  go run ./cmd/scraper       - correr bot dev"
                echo "  go build -o pedco-bot ./cmd/scraper"
                echo "  golangci-lint run          - lintear"
                echo "  sqlite3 pedcobot.db        - inspeccionar DB"
                echo "  openssl rand -base64 32    - generar SECRET_KEY"
                echo "================================================="
            '';
        };
    };
}
