{
  description = "Loom dev environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            # Go services
            go_1_26
            gopls
            golangci-lint
            gotools
            delve

            # Go/protobuf codegen
            protoc-gen-go
            protoc-gen-connect-go
            protoc-gen-go-grpc

            nodejs_24
            corepack_24

            # Proto codegen
            curl
            protobuf
            pkg-config
          ];

          # Corepack refuses to write shims into the read-only Nix store, so
          # point it at a project-local, gitignored directory instead.
          shellHook = "";
        };
      }
    );
}
