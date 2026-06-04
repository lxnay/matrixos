#!/bin/bash
# This is the main script that is executed inside the chroot for a seeder.
# It is responsible for executing the different phases of the seeding process, and
# for keeping track of which phases have been completed, so that if the process
# is interrupted, it can be resumed from the last completed phase.
# At the end of the process, the chroot should be in a state where it can be used as
# a base for derived seeders, or for generating artifacts (like a bootable image, a
# Content Delivery System release, etc).
#
# Variables exported by the worker (vector/lib/seeder/worker.go) and available in
# the environment:
#
# MATRIXOS_DEV_DIR
# The path to the matrixOS dev directory from within the chroot, which is the root of the
# matrixOS repository, and the main directory where seeders should read/write data.
#
# SEEDER_OVERLAY_GIT_REPO
# The Git repository URL for the seeder overlay, which is a repository containing the ebuilds
# and configs for the seeder. This is used by the chroot scripts to setup the
# Portage overlay for the seeder.
#
# DEFAULT_PRIVATE_GIT_REPO_PATH
# The directory path to the private git repository. This directory is expected to
# be already empty at this stage.
#
# SEEDERS_PHASES_STATE_DIR: the path to the directory where seeders can read/write files to
# keep track of which phases have been completed. This is used by the chroot.sh scripts
# to implement idempotency and resumability.
#
# SEEDER_DONE_FLAG_FILE_PREFIX
# The prefix for the seeder done flag file, which is used by the chroot.sh scripts
# to determine if a seeder has completed its process and the chroot is in a state
# that can be used as a base for derived seeders.
#
# SEEDER_DATE_CADENCE
# The cadence at which seeder chroots are versioned. This is used to determine the
# name of the chroot directory for the seeder, and to determine when to create a new
# chroot or reuse an existing one.
#
set -e
if [ -e /etc/profile ]; then
    source /etc/profile
fi
set -eu

source "${MATRIXOS_DEV_DIR}/build/seeders/lib/chroots_lib.sh"


BOOTSTRAP_PACKAGES=(
    app-eselect/eselect-repository
    dev-vcs/git
)
# TODO: maybe we can infer the kernel from the package list.
BUILD_KERNEL_PACKAGES=(
    sys-kernel/matrixos-kernel::matrixos
)
BUILD_KERNEL_INITRAMFS=(
    sys-kernel/matrixos-initramfs::matrixos
)
UPSTREAM_PORTAGE_REPOS=(
    matrixos
)

# Used by main() to get the initial portage counter value, to then
# determine which package was already (re)built and which not.
INITIAL_PORTAGE_COUNTER="0"

_seeder_name=$(basename "$(dirname "${0}")")


bedrock.system_bootstrap() {
    locale-gen
    emerge-webrsync --quiet

    # special bootstrapping command, skipping env-update.
    NO_ENV_UPDATE=1 chroots_lib.generic_build "${BOOTSTRAP_PACKAGES[@]}"
}

bedrock.buildenv_bootstrap() {
    chroots_lib.default_buildenv_bootstrap "${_seeder_name}"

    if [ "${INITIAL_PORTAGE_COUNTER}" = "0" ]; then
        echo "Initial Portage Counter is 0, this is unexpected." >&2
        return 1
    fi
}

bedrock._setup_matrixos_overlay() {
    if [ -z "${SEEDER_OVERLAY_GIT_REPO}" ]; then
        echo "bedrock._setup_matrixos_overlay: mssing parameter to setup_matrixos_overlay" >&2
        return 1
    fi

    # matrixos is not in repositories.xml yet.
    echo "Preparing matrixos overlay ..."
    eselect repository disable -f matrixos
    eselect repository add matrixos git "${SEEDER_OVERLAY_GIT_REPO}"
}

bedrock.portage_bootstrap() {
    bedrock._setup_matrixos_overlay
    chroots_lib.default_portage_bootstrap "${UPSTREAM_PORTAGE_REPOS[@]}"
}

bedrock.build_resolve_conflicts() {
    # Break circular dependencies
    USE="-gpm" chroots_lib.generic_build -1 sys-libs/ncurses:0
    # --update --newuse added to avoid pulling in sys-libs/zlib-ng prematurely
    chroots_lib.generic_build -1 --update --newuse virtual/libelf
    USE="-sysprof -avif -truetype" chroots_lib.generic_build -1 dev-libs/glib:2
    chroots_lib.generic_build -1 dev-libs/glib

    # Starting 2026-02-08, Gentoo stage3 are erroneously shipped with
    # too many Pythons.
    mapfile -t python_extra_vers < <(qlist -ISe dev-lang/python | sort | tail -n +2)
    echo "Found extra Python versions: ${python_extra_vers[@]}"
    chroots_lib.generic_build --depclean "${python_extra_vers[@]}"
}

bedrock.build_kernel() {
    chroots_lib.generic_build --newuse --update "${BUILD_KERNEL_PACKAGES[@]}" \
        "${BUILD_KERNEL_INITRAMFS[@]}"
}

bedrock.build_system() {
    # Rebuild for new use flags pulling in dependencies and build deps.
    local flags=(
        --newuse
        --deep
        --with-bdeps=y
    )
    local packages=(
        @system
    )
    chroots_lib.generic_build "${flags[@]}" "${packages[@]}"
}

bedrock.build_everything() {
    chroots_lib.default_build_everything "${_seeder_name}"
    # Trigger a rebuild of the initramfs so that we bundle the latest and
    # correct initramfs setup.
    chroots_lib.generic_forced_rebuild "${BUILD_KERNEL_INITRAMFS[@]}"

    echo "Initial portage counter was: ${INITIAL_PORTAGE_COUNTER}"
    echo "Packages with a counter greater than this, were built with matrixOS setup."
    echo "Packages with a counter lower than this need to be rebuilt. Rebuilds:"
    chroots_lib.rebuild_before_portage_counter "${INITIAL_PORTAGE_COUNTER}"
}

setup_portage_counter() {
    # Load portage counter from disk if we previously saved it.
    local stored_portage_counter=
    stored_portage_counter=$(chroots_lib.get_stored_portage_counter)
    if [ "${stored_portage_counter}" = "-1" ]; then
        # does not exist, initialize.
        local current_counter=
        current_counter=$(chroots_lib.get_current_portage_counter)
        echo "Initializing Portage counter at: ${current_counter}"
        chroots_lib.store_portage_counter "${current_counter}"
        INITIAL_PORTAGE_COUNTER="${current_counter}"
    else
        echo "Loaded Portage counter: ${stored_portage_counter}"
        INITIAL_PORTAGE_COUNTER="${stored_portage_counter}"
    fi
}

bedrock.tweak_nsswitch() {
    # make the default /etc/nsswitch.conf a bit less dumb
    # and add support for dns and mdns resolution.
    # This is done here because it's tied to the portage setup.
    sed -i '/^hosts:/c\hosts:      files myhostname mymachines dns mdns_minimal [NOTFOUND=return] resolve [!UNAVAIL=return]' \
        "/etc/nsswitch.conf"
}

main() {
    cd /

    chroots_lib.setup
    setup_portage_counter

    local phases=(
        bedrock.system_bootstrap
        bedrock.buildenv_bootstrap
        bedrock.portage_bootstrap
        bedrock.build_resolve_conflicts
        bedrock.build_kernel
        bedrock.build_system
        bedrock.build_everything
        bedrock.tweak_nsswitch
    )

    # Pre-run tests to check that for every phase we have a function declared
    for phase in "${phases[@]}"; do
        if ! declare -F "${phase}"; then
            echo "Function ${phase} does not exist." >&2
            return 1
        fi
    done

    for phase_f in "${phases[@]}"; do
        if ! chroots_lib.is_phase_done "${phase_f}"; then
            echo "Executing phase: ${phase_f} ..."
            "${phase_f}"
            chroots_lib.touch_done_phase "${phase_f}"
        else
            echo "${phase_f} already finished."
        fi
    done
}

main "${@}"