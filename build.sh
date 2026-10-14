#!/bin/bash

if [[ -z ${1:-} ]]; then
    echo "Usage: ./build.sh update"
    echo "       ./build.sh source"
    echo "       ./build.sh migrations"
    echo "       ./build.sh compile"
    echo "       ./build.sh amd64"
    echo "       ./build.sh packages <image>"
    exit 1
fi

set -euo pipefail

if [[ ${1} == "update" ]]; then
    rm -f cache-page*.json
    cp meta.json meta.json.bak
    # A failed command must not leave some keys bumped and others stale.
    trap 'mv meta.json.bak meta.json' ERR
    commands=$(jq -r 'to_entries[] | [(.key),.value] | join("=")' < meta.json | grep '__command')
    while read -r line; do
        key="${line%%=*}"
        key="${key%__command}"
        command="${line#*=}"
        value=$(eval "${command}")
        json=$(cat meta.json)
        jq --sort-keys --arg key "$key" --arg value "$value" '.[$key] = $value' <<< "${json}" > meta.json
        echo "Result: [${key}] [${value}]"
    done <<< "${commands}"
    trap - ERR
    rm -f meta.json.bak cache-page*.json
fi

if [[ ${1} == "source" ]]; then
    sonarr_repo=$(jq -re '.sonarr_repo' < meta.json)
    sonarr_sha=$(jq -re '.sonarr_sha' < meta.json)
    pr_repo=$(jq -re '.pr_repo' < meta.json)
    read -r -a pr_branches <<< "$(jq -re '.pr_branches' < meta.json)"
    read -r -a pr_shas <<< "$(jq -re '.pr_shas' < meta.json)"
    if (( ${#pr_branches[@]} != ${#pr_shas[@]} )); then
        echo "meta.json lists ${#pr_branches[@]} pr_branches but ${#pr_shas[@]} pr_shas: run ./build.sh update." >&2
        exit 1
    fi
    rm -rf _src
    git clone --quiet --filter=blob:none --no-checkout "https://github.com/${sonarr_repo}.git" _src
    git -C _src fetch --quiet "https://github.com/${pr_repo}.git" "${pr_shas[@]}"
    git -C _src checkout --quiet --detach "${sonarr_sha}"
    # Each branch carries its own .github/README.md and .coderabbit.yaml; keep the first so those never clash.
    git -C _src config merge.keepfirst.driver true
    printf '%s merge=keepfirst\n' .github/README.md .coderabbit.yaml > _src/.git/info/attributes
    # Merged in pr_branches order, so a failure names the branch that stopped fitting.
    for i in "${!pr_branches[@]}"; do
        if ! git -C _src -c user.name=build -c user.email=build@localhost merge --quiet --no-edit "${pr_shas[i]}"; then
            echo "${pr_repo} ${pr_branches[i]}@${pr_shas[i]:0:7} does not merge cleanly into ${sonarr_repo}@${sonarr_sha:0:7} after the branches listed before it: rebase it." >&2
            exit 1
        fi
    done
fi

if [[ ${1} == "source" || ${1} == "migrations" ]]; then
    sonarr_repo=$(jq -re '.sonarr_repo' < meta.json)
    sonarr_sha=$(jq -re '.sonarr_sha' < meta.json)
    # The live database records migrations by number only, so a PR migration at or below upstream's newest would be skipped or collide.
    migrations=src/NzbDrone.Core/Datastore/Migration
    upstream_max=$(git -C _src ls-tree --name-only "${sonarr_sha}" "${migrations}/" | sed -n 's|.*/\([0-9][0-9]*\)_[^/]*\.cs$|\1|p' | sort -n | tail -n 1)
    pr_numbers=$(git -C _src diff --name-only --diff-filter=A "${sonarr_sha}" HEAD -- "${migrations}/" | sed -n 's|.*/\([0-9][0-9]*\)_[^/]*\.cs$|\1|p' | sort -n)
    pr_min=$(sed -n 1p <<< "${pr_numbers}")
    if [[ -z ${upstream_max} ]]; then
        echo "No migrations found under ${migrations} in ${sonarr_repo}@${sonarr_sha:0:7}." >&2
        exit 1
    fi
    # 10# because 082 is not valid octal.
    if [[ -n ${pr_min} ]] && (( 10#${pr_min} <= 10#${upstream_max} )); then
        echo "PR migration ${pr_min} is not above upstream's ${upstream_max}: renumber it and fix the live database's VersionInfo first." >&2
        exit 1
    fi
    # Branches sharing a number merge cleanly, then FluentMigrator refuses to start.
    duplicates=$(uniq -d <<< "${pr_numbers}" | tr '\n' ' ')
    if [[ -n ${duplicates// /} ]]; then
        echo "PR migrations share a number (${duplicates% }): renumber one of them." >&2
        exit 1
    fi
    echo "Migrations: upstream ${sonarr_repo}@${sonarr_sha:0:7} up to ${upstream_max}, PR from ${pr_min:-none}"
fi

if [[ ${1} == "compile" ]]; then
    runtime=linux-musl-x64
    # Mirrors upstream's own CI: .github/workflows/build_v5.yml and .github/actions/build/action.yml.
    framework=$(sed -n 's/^ *FRAMEWORK: *\(net[0-9.]*\).*/\1/p' _src/.github/workflows/build_v5.yml)
    version=$(sed -n 's/^ *VERSION: *\([0-9.]*\).*/\1/p' _src/.github/workflows/build_v5.yml)
    if [[ -z ${framework} || -z ${version} ]]; then
        echo "Could not read FRAMEWORK and VERSION from _src/.github/workflows/build_v5.yml." >&2
        exit 1
    fi
    sed -i -e "s/<AssemblyVersion>[0-9.*]\+<\/AssemblyVersion>/<AssemblyVersion>${version}.${RUN_NUMBER:-0}<\/AssemblyVersion>/g" \
           -e "s/<AssemblyConfiguration>[\$()A-Za-z-]\+<\/AssemblyConfiguration>/<AssemblyConfiguration>$(jq -re '.version_branch' < meta.json)<\/AssemblyConfiguration>/g" \
           _src/src/Directory.Build.props
    # NuGet advisories are logged, not fatal: upstream suppresses them one at a time and a new one would stop every image update.
    (cd _src && dotnet msbuild -restore src/Sonarr.sln -p:SelfContained=true -p:Configuration=Release -p:Platform=Posix \
        -p:RuntimeIdentifiers="${runtime}" -p:EnableWindowsTargeting=true -p:WarningsNotAsErrors='NU1901%3BNU1902%3BNU1903%3BNU1904' -t:PublishAllRids)
    (cd _src && yarn install --frozen-lockfile && yarn build)
    rm -rf _artifacts && mkdir -p _artifacts/Sonarr
    cp -r "_src/_output/${framework}/${runtime}/publish/." _artifacts/Sonarr/
    cp -r _src/_output/UI _artifacts/Sonarr/UI
    tar -czf sonarr.tar.gz -C _artifacts Sonarr
fi

if [[ ${1} == "amd64" ]]; then
    while IFS= read -r line; do
        opts+=(--build-arg "$line")
    done <<< "$(jq -r 'to_entries[] | [(.key | ascii_upcase),.value] | join("=")' < meta.json | grep -v '__COMMAND')"
    image=$(basename "$(git rev-parse --show-toplevel)" | tr '[:upper:]' '[:lower:]')
    docker build --platform "linux/${1}" -f "./linux-${1}.Dockerfile" -t "${image}-${1}" "${opts[@]}" .
fi

if [[ ${1} == "packages" ]]; then
    image=${2:?Usage: ./build.sh packages <image>}
    docker run --quiet --rm --entrypoint="" -v "${PWD}/":/repo -e PACKAGES="/repo/packages.txt" "${image}" sh -c 'if grep -q alpine < /etc/os-release; then apk upgrade --no-cache --available >/dev/null 2>&1 && apk info --format json --fields name,version 2>/dev/null | jq -re '\''.[] | [.name, .version] | join("=")'\'' | sort > ${PACKAGES}; else export DEBIAN_FRONTEND=noninteractive && apt-get update >/dev/null 2>&1 && apt-get upgrade -y >/dev/null 2>&1 && dpkg-query --showformat="\${Package}=\${Version}\n" --show 2>/dev/null | sort > ${PACKAGES}; fi'
    upstream_image=$(jq -re '.upstream_image' < meta.json | sed 's/ghcr.io\///g')
    upstream_tag=$(jq -re '.upstream_tag' < meta.json)
    upstream_tag_sha=$(jq -re '.upstream_tag_sha' < meta.json)
    upstream_sha=${upstream_tag_sha//${upstream_tag}-/}
    upstream_names=$(curl -fsSL "https://raw.githubusercontent.com/${upstream_image}/${upstream_sha}/packages.txt" | cut -d= -f1)
    packages_txt=$(cat packages.txt)
    rm -f packages.txt && touch packages.txt
    while read -r line; do
        package="${line%%=*}"
        grep -qxF "${package}" <<< "${upstream_names}" || echo "${line}" >> packages.txt
    done <<< "${packages_txt}"
fi
