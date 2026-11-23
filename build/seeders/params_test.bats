#!/usr/bin/env bats
# Unit tests for build/seeders/*/params.sh

setup() {
    TEST_TMPDIR="$(mktemp -d)"

    REPO_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
    export MATRIXOS_DEV_DIR="${REPO_ROOT}"

    STUB_BIN="${TEST_TMPDIR}/bin"
    mkdir -p "${STUB_BIN}"

    # Date stub returning deterministic values (Wednesday of week starting 2025-06-09).
    cat > "${STUB_BIN}/date" << 'DATEOF'
#!/bin/bash
if [[ "$*" == *"+%u"* ]]; then
    echo "3"
elif [[ "$*" == *"-d"* ]] && [[ "$*" == *"+%Y%m%d"* ]]; then
    echo "20250609"
elif [[ "$*" == *"+%Y%m%d"* ]]; then
    echo "20250611"
elif [[ "$*" == *"+%Y%m01"* ]]; then
    echo "20250601"
else
    echo "20250611"
fi
DATEOF
    chmod +x "${STUB_BIN}/date"
    export PATH="${STUB_BIN}:${PATH}"

    # Env vars needed by finder functions.
    export SEEDER_DATE_CADENCE="weekly"
    export SEEDER_DONE_FLAG_FILE_PREFIX="/done-prefix"
    export SEEDERS_PHASES_STATE_DIR="/phases"
}

teardown() {
    rm -rf "${TEST_TMPDIR}"
}

# ===== 00-bedrock/params.sh =====

@test "bedrock params: SEEDER_DEPENDS is empty" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    [ "${SEEDER_DEPENDS}" = "" ]
}

@test "bedrock params: SEEDER_CHROOT_NAME has correct format" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    [[ "${SEEDER_CHROOT_NAME}" =~ ^bedrock-[0-9]{8}$ ]]
    [ "${SEEDER_CHROOT_NAME}" = "bedrock-20250609" ]
}

@test "bedrock params: SEEDER_CHROOTS_DIR is set" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    [ -n "${SEEDER_CHROOTS_DIR}" ]
    [[ "${SEEDER_CHROOTS_DIR}" == */out/seeder/chroots ]]
}

@test "bedrock params: PREFERRED_SEEDER_CHROOT_DIR includes chroot name" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    [[ "${PREFERRED_SEEDER_CHROOT_DIR}" == *"bedrock-20250609"* ]]
}

@test "bedrock params: find_latest_chroot_dir_for_derived_seeder returns latest" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    # Override chroots dir for testing.
    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250608"

    # Create done flags for both.
    local flag_dir_1="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir_1}"
    touch "${flag_dir_1}/done-prefix_myseeder"

    local flag_dir_2="${SEEDER_CHROOTS_DIR}/bedrock-20250608/phases"
    mkdir -p "${flag_dir_2}"
    touch "${flag_dir_2}/done-prefix_myseeder"

    run bedrock_params.find_latest_chroot_dir "myseeder"
    [ "$status" -eq 0 ]
    [[ "$output" == *"bedrock-20250608"* ]]
}

@test "bedrock params: find_all_chroot_dirs returns all valid dirs" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250608"

    local flag_dir_1="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir_1}"
    touch "${flag_dir_1}/done-prefix_myseeder"

    local flag_dir_2="${SEEDER_CHROOTS_DIR}/bedrock-20250608/phases"
    mkdir -p "${flag_dir_2}"
    touch "${flag_dir_2}/done-prefix_myseeder"

    run bedrock_params.find_all_chroot_dirs "myseeder"
    [ "$status" -eq 0 ]
    [[ "$output" == *"bedrock-20250601"* ]]
    [[ "$output" == *"bedrock-20250608"* ]]
}

@test "bedrock params: finder skips dirs without done flag" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250608"

    # Only create done flag for the first.
    local flag_dir="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    local result
    result=$(bedrock_params.find_latest_chroot_dir "myseeder" 2>/dev/null)
    [[ "${result}" == *"bedrock-20250601"* ]]
    [[ "${result}" != *"bedrock-20250608"* ]]
}

@test "bedrock params: finder fails when no valid dirs" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}"

    run bedrock_params.find_latest_chroot_dir "myseeder"
    [ "$status" -ne 0 ]
}

@test "bedrock params: finder ignores non-matching dir names" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-notadate"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"

    local flag_dir="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    run bedrock_params.find_latest_chroot_dir "myseeder"
    [ "$status" -eq 0 ]
    [[ "$output" == *"bedrock-20250601"* ]]
}

@test "bedrock params: _find_select_chroot_dirs_for_derived_seeder requires seeder_name" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    run bedrock_params._find_select_chroot_dirs_for_derived_seeder "" "bedrock" ""
    [ "$status" -eq 1 ]
}

@test "bedrock params: _find_select_chroot_dirs_for_derived_seeder requires prefix" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"
    run bedrock_params._find_select_chroot_dirs_for_derived_seeder "myseeder" "" ""
    [ "$status" -eq 1 ]
}

# ===== 10-server/params.sh =====

@test "server params: SEEDER_DEPENDS lists 00-bedrock" {
    source "${BATS_TEST_DIRNAME}/10-server/params.sh"
    [ "${SEEDER_DEPENDS}" = "00-bedrock" ]
}

@test "server params: SEEDER_CHROOT_NAME has correct format" {
    source "${BATS_TEST_DIRNAME}/10-server/params.sh"
    [[ "${SEEDER_CHROOT_NAME}" =~ ^server-[0-9]{8}$ ]]
    [ "${SEEDER_CHROOT_NAME}" = "server-20250609" ]
}

@test "server params: SEEDER_CHROOTS_DIR is set" {
    source "${BATS_TEST_DIRNAME}/10-server/params.sh"
    [ -n "${SEEDER_CHROOTS_DIR}" ]
}

# ===== 20-gnome/params.sh =====

@test "gnome params: SEEDER_DEPENDS lists 00-bedrock" {
    source "${BATS_TEST_DIRNAME}/20-gnome/params.sh"
    [ "${SEEDER_DEPENDS}" = "00-bedrock" ]
}

@test "gnome params: SEEDER_CHROOT_NAME has correct format" {
    source "${BATS_TEST_DIRNAME}/20-gnome/params.sh"
    [[ "${SEEDER_CHROOT_NAME}" =~ ^gnome-[0-9]{8}$ ]]
    [ "${SEEDER_CHROOT_NAME}" = "gnome-20250609" ]
}

@test "gnome params: SEEDER_CHROOTS_DIR is set" {
    source "${BATS_TEST_DIRNAME}/20-gnome/params.sh"
    [ -n "${SEEDER_CHROOTS_DIR}" ]
}

# ===== 21-cosmic/params.sh =====

@test "cosmic params: SEEDER_DEPENDS lists 00-bedrock" {
    source "${BATS_TEST_DIRNAME}/21-cosmic/params.sh"
    [ "${SEEDER_DEPENDS}" = "00-bedrock" ]
}

@test "cosmic params: SEEDER_CHROOT_NAME has correct format" {
    source "${BATS_TEST_DIRNAME}/21-cosmic/params.sh"
    [[ "${SEEDER_CHROOT_NAME}" =~ ^cosmic-[0-9]{8}$ ]]
    [ "${SEEDER_CHROOT_NAME}" = "cosmic-20250609" ]
}

@test "cosmic params: SEEDER_CHROOTS_DIR is set" {
    source "${BATS_TEST_DIRNAME}/21-cosmic/params.sh"
    [ -n "${SEEDER_CHROOTS_DIR}" ]
}

# ===== find_partial_chroot_dirs tests =====

@test "bedrock params: find_partial_chroot_dirs returns only incomplete dirs" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250608"

    # Mark only the first dir as completed.
    local flag_dir="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    local result
    result=$(bedrock_params.find_partial_chroot_dirs "myseeder" 2>/dev/null)
    # The incomplete dir should be included.
    [[ "${result}" == *"bedrock-20250608"* ]]
    # The completed dir should be excluded.
    [[ "${result}" != *"bedrock-20250601"* ]]
}

@test "bedrock params: find_partial_chroot_dirs fails when all dirs are complete" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"

    local flag_dir="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    run bedrock_params.find_partial_chroot_dirs "myseeder"
    [ "$status" -ne 0 ]
}

@test "bedrock params: find_all_chroot_dirs returns only complete dirs" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/bedrock-20250608"

    # Mark only the first dir as completed.
    local flag_dir="${SEEDER_CHROOTS_DIR}/bedrock-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    local result
    result=$(bedrock_params.find_all_chroot_dirs "myseeder" 2>/dev/null)
    [[ "${result}" == *"bedrock-20250601"* ]]
    [[ "${result}" != *"bedrock-20250608"* ]]
}

@test "bedrock params: _find_select find_partial for server prefix" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/server-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/server-20250608"

    local flag_dir="${SEEDER_CHROOTS_DIR}/server-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    # With find_partial, only incomplete dirs are returned.
    local result
    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "server" "1" "1" 2>/dev/null)
    [[ "${result}" == *"server-20250608"* ]]
    [[ "${result}" != *"server-20250601"* ]]

    # Without find_partial, only completed dirs are returned.
    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "server" "1" "" 2>/dev/null)
    [[ "${result}" == *"server-20250601"* ]]
    [[ "${result}" != *"server-20250608"* ]]
}

@test "bedrock params: _find_select find_partial for gnome prefix" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/gnome-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/gnome-20250608"

    local flag_dir="${SEEDER_CHROOTS_DIR}/gnome-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    local result
    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "gnome" "1" "1" 2>/dev/null)
    [[ "${result}" == *"gnome-20250608"* ]]
    [[ "${result}" != *"gnome-20250601"* ]]

    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "gnome" "1" "" 2>/dev/null)
    [[ "${result}" == *"gnome-20250601"* ]]
    [[ "${result}" != *"gnome-20250608"* ]]
}

@test "bedrock params: _find_select find_partial for cosmic prefix" {
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots"
    mkdir -p "${SEEDER_CHROOTS_DIR}/cosmic-20250601"
    mkdir -p "${SEEDER_CHROOTS_DIR}/cosmic-20250608"

    local flag_dir="${SEEDER_CHROOTS_DIR}/cosmic-20250601/phases"
    mkdir -p "${flag_dir}"
    touch "${flag_dir}/done-prefix_myseeder"

    local result
    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "cosmic" "1" "1" 2>/dev/null)
    [[ "${result}" == *"cosmic-20250608"* ]]
    [[ "${result}" != *"cosmic-20250601"* ]]

    result=$(bedrock_params._find_select_chroot_dirs_for_derived_seeder \
        "myseeder" "cosmic" "1" "" 2>/dev/null)
    [[ "${result}" == *"cosmic-20250601"* ]]
    [[ "${result}" != *"cosmic-20250608"* ]]
}

@test "bedrock params: trailing slash in MATRIXOS_DEV_DIR and SEEDER_CHROOTS_DIR does not produce double slashes" {
    export MATRIXOS_DEV_DIR="${REPO_ROOT}/"
    source "${BATS_TEST_DIRNAME}/00-bedrock/params.sh"

    [ "${SEEDER_CHROOTS_DIR}" = "${REPO_ROOT}/out/seeder/chroots" ]
    [ "${PREFERRED_SEEDER_CHROOT_DIR}" = "${REPO_ROOT}/out/seeder/chroots/bedrock-20250609" ]

    SEEDER_CHROOTS_DIR="${TEST_TMPDIR}/chroots/"
    mkdir -p "${TEST_TMPDIR}/chroots/bedrock-20250601/phases"
    touch "${TEST_TMPDIR}/chroots/bedrock-20250601/phases/done-prefix_myseeder"
    mkdir -p "${TEST_TMPDIR}/chroots/bedrock-20250608"

    local complete partial
    complete=$(bedrock_params.find_all_chroot_dirs "myseeder" 2>/dev/null)
    partial=$(bedrock_params.find_partial_chroot_dirs "myseeder" 2>/dev/null)

    [ "${complete}" = "${TEST_TMPDIR}/chroots/bedrock-20250601" ]
    [ "${partial}" = "${TEST_TMPDIR}/chroots/bedrock-20250608" ]
}

@test "derived params: trailing slash in MATRIXOS_DEV_DIR does not produce double slashes" {
    export MATRIXOS_DEV_DIR="${REPO_ROOT}/"
    for entry in "10-server:server" "20-gnome:gnome" "21-cosmic:cosmic"; do
        local dir="${entry%%:*}"
        local prefix="${entry##*:}"
        (
            source "${BATS_TEST_DIRNAME}/${dir}/params.sh"
            [ "${SEEDER_CHROOTS_DIR}" = "${REPO_ROOT}/out/seeder/chroots" ]
            [ "${PREFERRED_SEEDER_CHROOT_DIR}" = "${REPO_ROOT}/out/seeder/chroots/${prefix}-20250609" ]
        )
    done
}
