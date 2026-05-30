#!/bin/bash
set -eu

BUILD_FILE="/usr/share/matrixos/seeder/metadata/build"

_is_seed() {
    local seed_prefix="${1}"
    local seed_name=$(cat "${BUILD_FILE}" | grep SEED_NAME= 2>/dev/null || echo "")
    [[ "${seed_name}" == *"${seed_prefix}"* ]] && return 0
    return 1
}

_is_bedrock() {
    _is_seed "bedrock"
}

_is_server() {
    _is_seed "server"
}

_is_gnome() {
    _is_seed "gnome"
}

_is_cosmic() {
    _is_seed "cosmic"
}

_dump_logs() {
    echo "Collecting systemctl status for debugging:"
    systemctl status --no-pager &> /tmp/systemctl_status.log
    cat /tmp/systemctl_status.log

    echo "Listing failed units:"
    systemctl --failed &> /tmp/systemctl_failed.log
    cat /tmp/systemctl_failed.log

    echo "Journalctl -xb output:"
    journalctl -xb &> /tmp/journalctl.log
    cat /tmp/journalctl.log

    echo "blkid output:"
    blkid &> /tmp/blkid.log
    cat /tmp/blkid.log

    echo "mount output:"
    mount &> /tmp/mount.log
    cat /tmp/mount.log
}

test.start_sshd() {
    systemctl start sshd || {
        echo "Failed to start sshd service"
        return 1
    }
}

test.etc_resolv_conf() {
    local resolv_conf="/etc/resolv.conf"

    local i=
    local found=
    for i in {1..10}; do
        if [[ -f "${resolv_conf}" ]] && ( cat "${resolv_conf}" > /dev/null ); then
            cat "${resolv_conf}"
            echo "resolv.conf found: ${resolv_conf}"
            found=1
            break
        fi
        sleep 2
    done

    if [ -z "${found}" ]; then
        echo "resolv.conf not found or invalid: ${resolv_conf}"
        return 1
    fi

    grep -q "nameserver" "${resolv_conf}" || {
        echo "No nameserver entry found in ${resolv_conf}"
        return 1
    }
}

test.build_file() {
    if [[ ! -f "${BUILD_FILE}" ]]; then
        echo "Build file not found: ${BUILD_FILE}"
        return 1
    fi
}

test.os_release() {
    local os_release="/etc/os-release"
    if [[ ! -f "${os_release}" ]]; then
        echo "os-release file not found: ${os_release}"
        return 1
    fi
    if ! grep -q "ID=" "${os_release}"; then
        echo "ID field not found in ${os_release}"
        return 1
    fi
    local matrixos_id="matrixos"
    if ! grep -q "${matrixos_id}" "${os_release}"; then
        echo "ID does not contain ${matrixos_id} in ${os_release}"
        cat "${os_release}" || echo "Failed to read ${os_release}"
        return 1
    fi
}

test.portage() {
    if ! command -v emerge > /dev/null 2>&1; then
        echo "emerge command not found, Portage may be broken"
        return 1
    fi
    if [ ! -e "/etc/portage/make.conf" ]; then
        echo "Portage /etc/portage/make.conf not found"
        return 1
    fi
}

test.wait_boot_complete() {
    echo "Waiting for boot to complete..."
    systemctl is-system-running --wait || {
        local status
        status=$(systemctl is-system-running || echo "unknown")
        echo "System did not reach 'running' state, current state: ${status}"
        return 1
    }
    echo "Boot complete, system is running."
}

test.systemctl_status() {
    if ! systemctl is-active --quiet systemd-resolved; then
        echo "systemd-resolved service is not active"
        return 1
    fi

    local target
    target=$(systemctl get-default || true)
    if _is_bedrock || _is_server; then
        if ! systemctl is-active --quiet systemd-networkd; then
            echo "systemd-networkd service is not active"
            return 1
        fi
        if [[ "${target}" != "multi-user.target" ]]; then
            echo "Expected default target to be multi-user.target, but got ${target}"
            return 1
        fi
    fi
    if _is_gnome || _is_cosmic; then
        if ! systemctl is-active --quiet NetworkManager; then
            echo "NetworkManager service is not active"
            return 1
        fi
        if [[ "${target}" != "graphical.target" ]]; then
            echo "Expected default target to be graphical.target, but got ${target}"
            return 1
        fi
    fi
    if _is_gnome; then
        if ! systemctl is-active --quiet gdm; then
            echo "gdm service is not active"
            return 1
        fi
    fi
}

test.ostree_status() {
    if ! ostree admin status > /dev/null 2>&1; then
        echo "ostree admin status command failed"
        return 1
    fi

    local remotes=
    remotes=$(ostree remote list --show-urls || echo "")
    if [[ -z "${remotes}" ]]; then
        echo "No ostree remotes found"
        return 1
    fi
    if ! echo "${remotes}" | grep -q "origin"; then
        echo "Expected ostree remote 'origin' not found"
        return 1
    fi
    if ! echo "${remotes}" | grep -q "matrixos"; then
        echo "Expected ostree remote 'matrixos' not found"
        return 1
    fi
}

test.flatpak() {
    if _is_gnome || _is_cosmic; then
        if ! flatpak remotes -d | grep ^flathub; then
            echo "flatpak remotes command failed"
            return 1
        fi
    fi
}

test.setupOS() {
    vector setupOS \
        --skip-encryption \
        --skip-passwords \
        --locale en_US.UTF-8 \
        --keymap us \
        --timezone Europe/London \
        --hostname testvm-$RANDOM
}

test.jailbreak() {
    echo "Testing jailbreak functionality... This is a one way ticket to hell."
    vector jailbreak \
        --yolo-skip-full-branch-check \
        --yolo-skip-destroy-confirmation
}


main() {
    cd /

    local failed_tests=()
    local tests=(
        test.start_sshd
        test.wait_boot_complete
        test.etc_resolv_conf
        test.build_file
        test.os_release
        test.portage
        test.systemctl_status
        test.ostree_status
        test.flatpak
        test.setupOS
        test.jailbreak
    )

    local exit_st=0
    local test_f=
    for test_f in "${tests[@]}"; do
        if ! "${test_f}"; then
            echo "Test failed: ${test_f}"
            failed_tests+=("${test_f}")
            exit_st=1
        else
            echo "Test passed: ${test_f}"
        fi
    done

    if [ "${exit_st}" != "0" ]; then
        echo "One or more tests failed. Collecting logs for debugging."
        _dump_logs || true
        echo "Failed tests: ${failed_tests[*]}"
    fi

    return "${exit_st}"
}

main "${@}"