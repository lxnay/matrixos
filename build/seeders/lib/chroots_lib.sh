#!/bin/bash
# chroots_lib.sh - shared library between all the chroot seeders. This is also meant to share
#                  state between chroot.sh scripts and only executed inside seeding chroots.
set -eu

chroots_lib.cleanup() {
    # Final reap of any remaining zombies before exit.
    wait 2>/dev/null || true
}

# When running as PID 1 in a PID namespace (via unshare --pid --fork),
# orphaned child processes (e.g. grandchildren of emerge) get re-parented
# to us. We must reap them to prevent zombie accumulation.
# Bash internally calls waitpid(-1, WNOHANG) when processing SIGCHLD,
# which reaps all zombies including re-parented orphans. Installing a
# SIGCHLD trap ensures bash processes the signal promptly rather than
# deferring it until the next command boundary.
_ZOMBIE_REAPER_INSTALLED=0

chroots_lib._reap_zombies() {
    # wait with no args returns immediately if there are no bash-managed
    # background jobs, but the act of entering the trap handler already
    # triggered bash's internal waitpid(-1, WNOHANG) loop which reaps
    # all zombie children including re-parented orphans.
    wait 2>/dev/null || true
}

chroots_lib.setup_cleanup() {
    echo "Setting up EXIT trap for cleanup." >&2
    trap chroots_lib.cleanup EXIT
}

chroots_lib.setup_zombie_reaper() {
    echo "Installing SIGCHLD zombie reaper." >&2
    trap chroots_lib._reap_zombies CHLD
}

chroots_lib.setup_cancellation() {
    echo "Installing SIGTERM/SIGINT cancellation trap." >&2
    trap 'trap - TERM INT; echo "[!] External cancellation!"; kill -TERM 0 2>/dev/null; exit 130' TERM INT
}

chroots_lib.setup() {
    if [ "$$" = "1" ]; then
        echo "[!] PID 1 detected. Arming namespace traps..." >&2

        # Sequence A: The EXIT Trap
        # This must be defined first so it is guaranteed to fire regardless 
        # of whether the script ends naturally or is forced to exit by another trap.
        trap chroots_lib.cleanup EXIT

        # Sequence B: The Cancellation Trap (TERM / INT)
        # 1. 'trap - TERM INT' immediately disarms this trap to prevent recursive loops from kill -0.
        # 2. 'kill -TERM 0' blasts the signal to the process group, killing the foreground 'emerge'.
        # 3. 'exit 130' terminates Bash, which automatically triggers Sequence A (EXIT).
        chroots_lib.setup_cancellation

        # Sequence C: The Zombie Trap (CHLD)
        # Installed last so it doesn't accidentally catch background tasks spawned
        # during the setup phase itself.
        chroots_lib.setup_zombie_reaper
    else
        echo "[!] Not PID 1 (PID=$$). Skipping namespace traps." >&2
    fi

    echo "Dump of /proc/self/mountinfo:" >&2
    cat /proc/self/mountinfo >&2
    echo "PID 1 is:" >&2
    readlink /proc/1/exe >&2
    echo "Cgroup state:" >&2
    echo "Memory limit (cgroup v2): $(cat /sys/fs/cgroup/memory.max 2>/dev/null || echo 'N/A')" >&2
    echo "CPU quota (cgroup v2): $(cat /sys/fs/cgroup/cpu.max 2>/dev/null || echo 'N/A')" >&2
    echo "CPU set (cgroup v2): $(cat /sys/fs/cgroup/cpuset.cpus.effective 2>/dev/null || echo 'N/A')" >&2
}

_get_phase_path() {
    echo "${SEEDERS_PHASES_STATE_DIR}/${1}.done"
}

chroots_lib.touch_done_phase() {
    local phase_path=
    phase_path="$(_get_phase_path "${1}")"
    mkdir -p "$(dirname "${phase_path}")"
    touch "${phase_path}"
}

chroots_lib.is_phase_done() {
    local phase_path=
    phase_path="$(_get_phase_path "${1}")"
    echo "Checking if phase is already done: ${phase_path}"
    test -f "${phase_path}"
}

chroots_lib.package_list_path() {
    local seeder_name="${1}"
    if [ -z "${seeder_name}" ]; then
        echo "${0}: missing seeder name parameter" >&2
        return 1
    fi
    echo "${MATRIXOS_DEV_DIR}/build/seeders/${seeder_name}/packages.conf"
}

chroots_lib.portage_confdir_path() {
    local seeder_name="${1}"
    if [ -z "${seeder_name}" ]; then
        echo "${0}: missing seeder name parameter" >&2
        return 1
    fi
    echo "${MATRIXOS_DEV_DIR}/build/seeders/${seeder_name}/portage"
}

chroots_lib.validate_package_list_path() {
    local path="${1}"
    if [ -z "${path}" ]; then
        echo "${0}: missing parameter" >&2
        return 1
    fi
    if [ ! -e "${path}" ]; then
        echo "${path} does not exist." >&2
        return 1
    fi
}

chroots_lib.validate_portage_confdir_path() {
    local path="${1}"
    if [ -z "${path}" ]; then
        echo "${0}: missing parameter" >&2
        return 1
    fi
    if [ ! -d "${path}" ]; then
        echo "${path} does not exist." >&2
        return 1
    fi
}

chroots_lib.validate_matrixos_git_repo() {
    # matrixos.git is taken care of by seeder.sh, but check.
    local matrixos_dev_dir_flag="${MATRIXOS_DEV_DIR}/.matrixos"
    if [ ! -f "${matrixos_dev_dir_flag}" ]; then
        echo "${matrixos_dev_dir_flag} does not exist. matrixos.git must be cloned into ${MATRIXOS_DEV_DIR}." >&2
        return 1
    fi
}

_mos_private_message() {
    echo "Please set it in conf/matrixos.conf, matrixOS.PrivateGitRepoPath." >&2
    echo "See README.md and https://github.com/lxnay/matrixos-private-example for more details." >&2
    echo "This directory contains YOUR GPG private keys and SecureBoot certs necessary to build" >&2
    echo "and release a custom matrixOS Gentoo build." >&2
}

chroots_lib.check_matrixos_private() {
    local matrixos_private="${1}"
    if [ -z "${matrixos_private}" ]; then
        echo "matrixOS.PrivateGitRepoPath is empty ..." >&2
        _mos_private_message
        return 1
    fi
    if [ ! -d "${matrixos_private}" ]; then
        echo "${matrixos_private} does not exist ..." >&2
        _mos_private_message
        return 1
    fi
}

chroots_lib.validate_matrixos_private() {
    # Inside chroots, we always place matrixos-private into /etc/matrixos-private.
    # This is because many pieces of the codebase, including the Portage config,
    # expect it to be there.
    local matrixos_private="${DEFAULT_PRIVATE_GIT_REPO_PATH}"
    chroots_lib.check_matrixos_private "${matrixos_private}"

    # This is usually bind-mount. Make sure it is and not
    # copied over.
    local mounted=
    mounted=$(findmnt -n -o TARGET "${matrixos_private}")
    if [ "${mounted}" != "${matrixos_private}" ]; then
        echo "${matrixos_private} is not a bind-mount. seeder should do this." >&2
        return 1
    fi
}

chroots_lib.default_portage_bootstrap() {
    for repo in "${@}"; do
        eselect repository enable "${repo}"
    done
    for repo in "${@}"; do
        emaint --repo="${repo}" sync
    done
    echo "Portage bootstrap complete with repos: ${*}"
    echo "Preparing binhost for use..."
    emaint binhost -f || true
}

chroots_lib.default_buildenv_bootstrap() {
    local seeder_name="${1}"
    if [ -z "${seeder_name}" ]; then
        echo "${0}: missing seeder name parameter" >&2
        return 1
    fi
    local package_list_path=
    package_list_path=$(chroots_lib.package_list_path "${seeder_name}")
    chroots_lib.validate_package_list_path "${package_list_path}"

    local portage_confdir_path=
    portage_confdir_path="$(chroots_lib.portage_confdir_path "${seeder_name}")"
    chroots_lib.validate_portage_confdir_path "${portage_confdir_path}"
    chroots_lib.validate_matrixos_git_repo

    # Set up portage config.
    local etcportage="/etc/portage"
    rm -rf "${etcportage}"
    ln -sf "..${portage_confdir_path}" "${etcportage}"
    echo "New ${etcportage} path:"
    ls -la "${etcportage}"
    if ! [[ -L "${etcportage}" && -d "${etcportage}" ]]; then
        echo "${etcportage} is not a valid directory symlink." >&2
        return 1
    fi
    # Setup and validate matrixos-private.
    chroots_lib.validate_matrixos_private
}

chroots_lib.default_build_everything() {
    local seeder_name="${1}"
    if [ -z "${seeder_name}" ]; then
        echo "${0}: missing seeder name parameter" >&2
        return 1
    fi
    local package_list_path=
    package_list_path=$(chroots_lib.package_list_path "${seeder_name}")
    chroots_lib.validate_package_list_path "${package_list_path}"

    local packages=()
    # Read the file, skip comments and spaces.
    readarray -t packages < <(grep -vE '^[[:space:]]*($|#)' "${package_list_path}")
    echo "Building the package list, containing:"
    local p=
    for p in "${packages[@]}"; do
        echo ">> ${p}"
    done
    chroots_lib.generic_build --update --newuse --deep -v "${packages[@]}"
}

chroots_lib.default_clean_temporary_artifacts() {
    echo "Cleaning temporary artifacts ..."
    dirs=(
        /var/tmp/portage
        /var/cache/revdep-rebuild
        /root/.cache
    )
    for d in "${dirs[@]}"; do
        echo "Removing ${d} inside chroot ..."
        rm -rf "${d}"
    done

    # /var/cache/distfiles, binpkgs should be bind mount. /var/db/repos is copied over.
    env-update
}

chroots_lib.rebuild_before_portage_counter() {
    local counter="${1}"
    if [ -z "${counter}" ]; then
        echo "${0}: missing parameter to rebuild_before_portage_counter" >&2
        return 1
    fi

    local rebuilds=()
    local cntfile=
    local cnt=
    local cntdir=
    local cat=
    local pf=
    local repo=
    local pkg=
    local atom=
    local pn=
    for cntfile in /var/db/pkg/*/*/COUNTER; do
        cnt=$(cat "${cntfile}")
        if [[ ${cnt} -le ${counter} ]]; then
            cntdir=$(dirname "${cntfile}")
            pf=$(basename "${cntdir}")
            cat=$(basename "$(dirname "${cntdir}")")
            repo=$(cat "${cntdir}/repository")
            atom="${cat}/${pf}"
            slot=$(portageq metadata / installed "${atom}" SLOT)
            pn=$(portageq metadata / installed "${atom}" PN)
            pkg="${cat}/${pn}:${slot}"
            echo ">> ${pkg}"
            rebuilds+=( "${pkg}" )
        fi
    done
    if [[ ${#rebuilds[@]} -gt 0 ]]; then
        chroots_lib.generic_build -v --oneshot "${rebuilds[@]}"
    else
        echo "No packages to rebuild."
    fi
}

_get_counter_path() {
    local counter_path="${SEEDERS_PHASES_STATE_DIR}/.portage_counter.tmp"
    echo "${counter_path}"
}

chroots_lib.store_portage_counter() {
    local counter="${1}"
    if [ -z "${counter}" ]; then
        echo "${0}: missing parameter to store_portage_counter" >&2
        return 1
    fi

    local counter_path=
    counter_path=$(_get_counter_path)
    mkdir -p "$(dirname "${counter_path}")"
    echo "${counter}" > "${counter_path}"
}

chroots_lib.get_stored_portage_counter() {
    local counter_path=
    counter_path=$(_get_counter_path)
    if [ -f "${counter_path}" ]; then
        cat "${counter_path}"
    else
        echo -en "-1"
    fi
}

chroots_lib.get_current_portage_counter() {
    (for f in /var/db/pkg/*/*/COUNTER; do cat "${f}"; echo; done) | sort -n | tail -n 1
}

chroots_lib.try_get_cgroup_cpuset() {
    # When cpuset is active, nproc reflects the pinned cores via
    # sched_getaffinity.  Check cpuset.cpus.effective to confirm
    # cpuset is actually constraining us.
    local cg_cpuset="/sys/fs/cgroup/cpuset.cpus.effective"
    if [ ! -f "${cg_cpuset}" ]; then
        echo "No cgroup cpuset found at ${cg_cpuset}. Cannot determine CPU count via cpuset." >&2
        return 1
    fi

    local num_procs=
    num_procs=$(nproc 2>/dev/null || true)
    if [ -z "${num_procs}" ] || [ "${num_procs}" -lt 1 ] 2>/dev/null; then
        echo "nproc returned invalid value despite cpuset being present." >&2
        return 1
    fi

    echo "${num_procs}"
}

chroots_lib.try_get_cgroup_cpu_max() {
    # Determine effective CPU count.
    # cpu.max (format: "$MAX $PERIOD" in µs) gives the bandwidth limit;
    # effective CPUs = max / period.  nproc only reflects cpuset, not
    # cpu bandwidth, so we prefer cpu.max when present.
    local num_procs=
    local cg_cpu_max="/sys/fs/cgroup/cpu.max"
    if [ ! -f "${cg_cpu_max}" ]; then
        echo "No cgroup cpu.max found at ${cg_cpu_max}. Cannot determine CPU quota via cpu.max." >&2
        return 1
    fi

    local cpu_raw=
    cpu_raw=$(cat "${cg_cpu_max}" 2>/dev/null || true)
    local quota= period=
    quota=$(echo "${cpu_raw}" | awk '{print $1}')
    period=$(echo "${cpu_raw}" | awk '{print $2}')
    if [ -n "${quota}" ] && [ "${quota}" != "max" ] && [ -n "${period}" ] && [ "${period}" -gt 0 ] 2>/dev/null; then
        num_procs=$(( quota / period ))
        if [ "${num_procs}" -lt 1 ]; then
            num_procs=1
        fi
    fi

    echo "${num_procs}"
}

chroots_lib.try_get_cgroup_memory_max() {
    # Inside the cgroup namespace, /sys/fs/cgroup/memory.max is visible
    # and shows the per-worker limit (same mechanism Docker uses).
    # Fall back to free(1) for unconstrained or non-cgroup runs.
    local num_gib=
    local cg_max="/sys/fs/cgroup/memory.max"
    if [ ! -f "${cg_max}" ]; then
        echo "No cgroup memory.max found at ${cg_max}. Assuming unconstrained memory." >&2
        return 1
    fi

    local max_bytes=
    max_bytes=$(cat "${cg_max}" 2>/dev/null || true)
    if [ -n "${max_bytes}" ] && [ "${max_bytes}" != "max" ]; then
        num_gib=$(( max_bytes / 1073741824 ))
        if [ "${num_gib}" -lt 1 ]; then
            num_gib=1
        fi
    fi
    echo "${num_gib}"
}

chroots_lib._try_get_procs() {
    # Prefer cpuset (reflects pinned cores via nproc) over cpu.max
    # (bandwidth throttling).  Fall back to nproc if neither is set.
    local num_procs=
    num_procs=$(chroots_lib.try_get_cgroup_cpuset || true)
    if [ -z "${num_procs}" ]; then
        num_procs=$(chroots_lib.try_get_cgroup_cpu_max || true)
    fi
    if [ -z "${num_procs}" ]; then
        echo "No cgroup CPU constraints detected. Using nproc to determine CPU count." >&2
        num_procs=$(nproc 2>/dev/null || true)
    fi

    local num_gib=
    num_gib=$(chroots_lib.try_get_cgroup_memory_max || true)

    if [ -z "${num_gib}" ]; then
        echo "No cgroup memory constraints detected. Using free(1) to determine total memory." >&2
        num_gib=$(free -g | awk '/^Mem:/{print $2}' || true)
    fi

    # Assume 1C/2G.
    if [ -z "${num_procs}" ] || [ -z "${num_gib}" ]; then
        echo "WARNING: Could not determine number of processors or amount of memory. Using default 2C/4G." >&2
        num_procs=2
        num_gib=4
    else
        # Normalize num_procs based on memory, to avoid OOMs.
        # For example, on a 4GiB RAM machine, we don't want to
        # spawn 8 emerge processes just because there are 8 cores.
        local num_gib_procs=
        num_gib_procs=$(( num_gib / 2 ))
        # If num_gib_procs is odd, make it even
        if [ $(( num_gib_procs % 2 )) -ne 0 ]; then
            num_gib_procs=$(( num_gib_procs + 1 ))
        fi
        if [ "${num_gib_procs}" -lt "${num_procs}" ]; then
            echo "Limiting emerge jobs to ${num_gib_procs} based on available memory (${num_gib} GiB)." >&2
            num_procs="${num_gib_procs}"
        fi
        echo "Determined emerge jobs flags: --jobs=${num_procs} --load-average=${num_procs}" >&2
    fi

    echo "${num_procs}"
}

chroots_lib.try_get_emerge_jobs_flags() {
    local num_procs="${1}"
    local flags=()
    if [ -n "${num_procs}" ]; then
        flags+=(
            --jobs="${num_procs}"
            --load-average="${num_procs}"
        )
    fi
    echo "${flags[@]}"
}

chroots_lib.emerge_common_args() {
    local num_procs="${1}"

    local jobs_flags
    read -ra jobs_flags <<< "$(chroots_lib.try_get_emerge_jobs_flags "${num_procs}")"
    local args=(
        --backtrack=100
        --binpkg-respect-use=y
        --buildpkg
        --usepkg
        --usepkg-exclude-live=y
        --quiet-build=y
        --verbose
    )
    echo "${args[@]}" "${jobs_flags[@]}"
}

chroots_lib.emerge_common_rebuild_args() {
    local num_procs="${1}"

    local jobs_flags
    read -ra jobs_flags <<< "$(chroots_lib.try_get_emerge_jobs_flags "${num_procs}")"
    local args=(
        --quiet-build=y
        --verbose
    )
    echo "${args[@]}" "${jobs_flags[@]}"
}

chroots_lib.generic_build() {
    if [ -z "${NO_ENV_UPDATE:-}" ]; then
        env-update
    fi
    local num_procs=$(chroots_lib._try_get_procs)

    local common_args
    read -ra common_args <<< "$(chroots_lib.emerge_common_args "${num_procs}")"

    echo ">> emerge" "${common_args[@]}" "${@}"
    MAKEOPTS="-j${num_procs} -l${num_procs}" \
    NINJAOPTS="-j${num_procs} -l${num_procs}" \
        emerge "${common_args[@]}" "${@}"
}

chroots_lib.generic_forced_rebuild() {
    env-update
    local num_procs=$(chroots_lib._try_get_procs)

    local common_args
    read -ra common_args <<< "$(chroots_lib.emerge_common_rebuild_args "${num_procs}")"

    echo ">> emerge (forcing rebuild)" "${common_args[@]}" "${@}"
    MAKEOPTS="-j${num_procs} -l${num_procs}" \
    NINJAOPTS="-j${num_procs} -l${num_procs}" \
        emerge "${common_args[@]}" "${@}"
}

chroots_lib.clean_old_distfiles() {
    local distdir="/var/cache/distfiles"
    local ttl_days="30"

    # Guard: if noatime is active, atime is never updated so -atime based
    # eviction would incorrectly delete recently-used packages.
    local mount_opts=
    mount_opts=$(findmnt -n -o OPTIONS --target "${distdir}" 2>/dev/null || true)
    if [[ ",${mount_opts}," == *",noatime,"* ]]; then
        echo "[!] WARNING: ${distdir} is mounted with noatime. Skipping atime-based eviction." >&2
        echo "[!] Remount without noatime (relatime is fine) for distfiles cache eviction to work." >&2
        return 0
    fi

    echo "[*] Sweeping ${distdir} for distfiles unread in ${ttl_days} days..."

    # Evict stale source tarballs based on access time (atime).
    # Distfiles are plain archives; no Portage index to rebuild afterwards.
    find "${distdir}" -type f \
        -atime +"${ttl_days}" \
        -print -delete

    # Prune empty subdirectories (some mirrors/fetchers create them).
    find "${distdir}" -mindepth 1 -type d -empty -delete

    echo "[*] Distfiles cache eviction complete."
}

chroots_lib.clean_old_binpkgs() {
    # This script relies on the fact that the BINPKG_DIR is bind-mounted without noatime.
    local pkgdir="/var/cache/binpkgs"
    local ttl_days="30"

    # Guard: if noatime is active, atime is never updated so -atime based
    # eviction would incorrectly delete recently-used packages.
    local mount_opts=
    mount_opts=$(findmnt -n -o OPTIONS --target "${pkgdir}" 2>/dev/null || true)
    if [[ ",${mount_opts}," == *",noatime,"* ]]; then
        echo "[!] WARNING: ${pkgdir} is mounted with noatime. Skipping atime-based eviction." >&2
        echo "[!] Remount without noatime (relatime is fine) for binpkg cache eviction to work." >&2
        return 0
    fi

    echo "[*] Sweeping ${pkgdir} for binpkgs unread in ${ttl_days} days..."

    # Evict stale binary packages based strictly on access time (atime).
    # (Note: If your kernel defaults to 'relatime', atime only updates if the 
    # previous atime was earlier than mtime/ctime, or if 24 hours have passed. 
    # For a multi-day TTL, relatime is perfectly sufficient).
    find "${pkgdir}" -type f \
        \( -name "*.tbz2" -o -name "*.gpkg.tar" -o -name "*.xpak" \) \
        -atime +"${ttl_days}" \
        -print -delete

    # Prune orphaned category directories left behind.
    find "${pkgdir}" -mindepth 1 -type d -empty -delete

    # Synchronize the Portage index.
    emaint binhost --fix
    echo "[*] Binary packages cache eviction complete."
}
