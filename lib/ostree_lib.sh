#!/bin/bash
# matrixOS ostree integration library.
set -eu

source "${MATRIXOS_DEV_DIR:-/matrixos}/headers/env.include.sh"
source "${MATRIXOS_DEV_DIR}"/image/headers/imagerenv.include.sh


ostree_lib.branch_contains_remote() {
    local branch="${1}"
    if [ -z "${branch}" ]; then
        echo "ostree_lib.branch_contains_remote: missing branch parameter." >&2
        return 1
    fi

    local b=${branch%:.*}
    if [ "${b}" = "${branch}" ]; then
        return 0
    fi
    return 1
}

ostree_lib.extract_remote_from_ref() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.extract_remote_from_ref: missing ref parameter." >&2
        return 1
    fi

    local remote="${ref%:*}"
    if [ "${remote}" != "${ref}" ]; then
        echo "${remote}"
    fi
}

ostree_lib.clean_remote_from_ref() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.clean_remote_from_ref: missing ref parameter." >&2
        return 1
    fi
    echo "${ref#*:}"
}

ostree_lib.is_branch_shortname() {
    local branch="${1}"
    if [ "$(basename "${branch}")" = "${branch}" ]; then
        return 0
    fi
    return 1
}

ostree_lib.is_branch_full_suffixed() {
    local branch="${1}"
    [ -z "${branch}" ] && return 1

    if [[ "${branch}" == *"-${MATRIXOS_OSTREE_FULL_SUFFIX}" ]]; then
        return 0
    fi
    return 1
}

ostree_lib.branch_shortname_to_normal() {
    local branch_rel_stage="${1}"
    if [ -z "${branch_rel_stage}" ]; then
        echo "${0} missing rel stage parameter." >&2
        return 1
    fi

    # normal as in, not the "-full" tree branch with dev libs etc.
    local branch_shortname="${2}"
    if [ -z "${branch_shortname}" ]; then
        echo "${0} missing branch parameter." >&2
        return 1
    fi

    local name_arch="${MATRIXOS_OSNAME}/${MATRIXOS_ARCH}"
    if [ "${branch_rel_stage}" = "prod" ]; then
        echo "${name_arch}/${branch_shortname}"
    else
        echo "${name_arch}/${branch_rel_stage}/${branch_shortname}"
    fi
}

ostree_lib.branch_shortname_to_full() {
    local branch_shortname="${1}"
    local branch_rel_stage="${2}"
    branch_shortname="${branch_shortname}-${MATRIXOS_OSTREE_FULL_SUFFIX}"
    ostree_lib.branch_shortname_to_normal "${branch_rel_stage}" "${branch_shortname}"
}

ostree_lib.branch_to_full() {
    local branch="${1}"
    ! ostree_lib.is_branch_full_suffixed "${branch}"

    echo "${branch}-${MATRIXOS_OSTREE_FULL_SUFFIX}"
}

ostree_lib.remove_full_from_branch() {
    local branch="${1}"
    if [ -z "${branch}" ]; then
        echo "ostree_lib.remove_full_from_branch: missing branch parameter." >&2
        return 1
    fi

    if ostree_lib.is_branch_full_suffixed "${branch}"; then
        echo "${branch%-${MATRIXOS_OSTREE_FULL_SUFFIX}}"
    else
        echo "${branch}"
    fi
}

ostree_lib.run_strict() {
    ostree "${@}"
}

ostree_lib.run() {
    local verbose_args=()
    if [ "${OSTREE_LIB_VERBOSE_MODE:-}" = "1" ]; then
        echo -en ">> Executing: ostree " >&2
        echo -en "${@}" >&2
        echo >&2
        verbose_args+=( --verbose )
    fi
    ostree_lib.run_strict "${verbose_args[@]}" "${@}"
}

ostree_lib.collection_id_args() {
    local args=()
    if [ -n "${MATRIXOS_OSTREE_COLLECTION_ID}" ]; then
        args+=(
            --collection-id="${MATRIXOS_OSTREE_COLLECTION_ID}"
        )
    fi
    echo "${args[@]}"
}

ostree_lib.get_gpg_pubkey_path() {
    echo "${MATRIXOS_OSTREE_GPG_PUB_PATH}"
}

ostree_lib.ostree_gpg_args() {
    local gpg_enabled="${1}"
    if [ -z "${gpg_enabled}" ]; then
        return 0
    fi

    local gpg_args=(
        --gpg-sign="$(ostree_lib.get_ostree_gpg_key_id)"
        --gpg-homedir="$(ostree_lib.get_ostree_gpg_homedir)"
    )
    echo "${gpg_args[@]}"
}

ostree_lib.ostree_client_side_gpg_args() {
    local gpg_enabled="${1}"

    local gpg_args=()
    if [ -z "${gpg_enabled}" ]; then
        gpg_args+=( --no-gpg-verify )
    else
        gpg_args+=(
            --set=gpg-verify=true
            --gpg-import="$(ostree_lib.get_gpg_pubkey_path)"
        )
    fi
    echo "${gpg_args[@]}"
}

ostree_lib.setup_etc() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "${0}: missing imagedir parameter." >&2
        return 1
    fi

    # strip off last / if it exists.
    imagedir="${imagedir%/}"

    echo "Setting up /etc..."
    local etcdir="${imagedir}/etc"
    local usretcdir="${imagedir}/usr/etc"
    echo "Moving ${etcdir} to ${usretcdir}"
    mv "${etcdir}" "${usretcdir}"
}

ostree_lib.prepare_filesystem_hierarchy() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "${0}: missing imagedir parameter." >&2
        return 1
    fi

    # strip off last / if it exists.
    imagedir="${imagedir%/}"

    # The image dir must contain /sysroot
    mkdir "${imagedir}/sysroot"
    ln -s sysroot/ostree "${imagedir}/ostree"

    # And /tmp should be a symlink to sysroot/tmp.
    mv "${imagedir}/tmp" "${imagedir}/sysroot/tmp"
    ln -s "sysroot/tmp" "${imagedir}/tmp"

    # Clean up /etc/machine-id in case it's there.
    rm -f "${imagedir}/etc/machine-id"
    touch "${imagedir}/etc/machine-id"

    ostree_lib.setup_etc "${imagedir}"

    echo "Setting up /var/db/pkg..."
    local vardbpkg="${imagedir}/var/db/pkg"
    local relusrvardbpkg="${MATRIXOS_RO_VDB#/}"
    local usrvardbpkg="${imagedir}/${relusrvardbpkg}"
    echo "Moving ${vardbpkg} to ${usrvardbpkg}"
    mv "${vardbpkg}" "${usrvardbpkg}"
    ln -s "../../${relusrvardbpkg}" "${vardbpkg}"

    echo "Setting up /opt..."
    local optdir="${imagedir}/opt"
    if [ -d "${optdir}" ]; then
        if [ -e "${imagedir}/var/opt" ]; then
            rm -v "${imagedir}/var/opt"
            ## will fail if dir, on purpose.
        fi
        mv "${optdir}" "${imagedir}/var/opt"
    elif [ -e "${optdir}" ]; then
        rm -v "${optdir}"
    fi
    ln -s "var/opt" "${optdir}"

    echo "Setting up /srv..."
    local srvdir="${imagedir}/srv"
    local varsrvdir="${imagedir}/var/srv"

    if [ -d "${srvdir}" ]; then
        if [ -e "${varsrvdir}" ]; then
            rm -v "${varsrvdir}"
            ## will fail if dir, on purpose.
        fi
        mv "${srvdir}" "${varsrvdir}"
    elif [ -e "${srvdir}" ]; then
        rm -v "${srvdir}"
    fi

    if [ ! -d "${varsrvdir}" ]; then
        mkdir -p "${varsrvdir}"
    fi
    ln -s "var/srv" "${srvdir}"

    echo "Setting up /snap ..."
    local snapdir="${imagedir}/snap"
    mkdir -p "${snapdir}"

    echo "Setting up /usr/src (for snap) ..."
    local usrsrcdir="${imagedir}/usr/src"
    mkdir -p "${usrsrcdir}"

    echo "Setting up /home ..."
    local homedir="${imagedir}/home"
    local varhomedir="${imagedir}/var/home"
    if [[ -L "${homedir}" ]] && [[ -d "${varhomedir}" ]]; then
        local homelink=
        homelink=$(readlink -f "${homedir}")
        if [ "${homelink}" = "var/home" ]; then
            echo "${homedir} is a symlink and ${varhomedir} is a directory. All good."
        else
            echo "${homedir} symlink points to an unexpected path: ${homelink}" >&2
            return 1
        fi
    elif [[ -d "${homedir}" ]] && [[ ! -L "${homedir}" ]]; then
        if [ -e "${varhomedir}" ]; then
            echo "WARNING: removing ${varhomedir}"
            rm -rf "${varhomedir}"
        fi
        mv "${homedir}" "${varhomedir}"
    elif [ -e "${homedir}" ]; then
        rm -v "${homedir}"
    fi
    ln -s "var/home" "${homedir}"

    echo "Setting up ${MATRIXOS_EFI_ROOT}..."
    local efiroot="${imagedir}/${MATRIXOS_EFI_ROOT}"
    mkdir -p "${efiroot}"

    echo "Setting up /usr/local..."
    local usrlocaldir="${imagedir}/usr/local"
    local relusrlocal="var/usrlocal"
    mv "${usrlocaldir}" "${imagedir}/${relusrlocal}"
    ln -s "../${relusrlocal}" "${usrlocaldir}"
}

ostree_lib.boot_commit() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "Missing parameter to ostree_lib.boot_commit" >&2
        return 1
    fi
    local ostree_boot_prefix="${sysroot}/ostree/boot.1/${MATRIXOS_OSNAME}"
    local ostree_boot_commit=
    ostree_boot_commit=$(ls -1 "${ostree_boot_prefix}")
    echo "${ostree_boot_commit}"
}

ostree_lib.get_ostree_gpg_key_id() {
    gpg --homedir="$(ostree_lib.get_ostree_gpg_homedir)" \
        --batch --yes \
        --with-colons --show-keys --keyid-format LONG \
        "$(ostree_lib.get_gpg_pubkey_path)" | grep "^pub" | cut -d: -f5
}

ostree_lib.get_ostree_gpg_homedir() {
    local homedir="${MATRIXOS_OSTREE_DEV_GPG_HOMEDIR}"
    echo ${homedir}
}

ostree_lib.import_gpg_key() {
    local key_path="${1}"
    if [ -z "${key_path}" ]; then
        echo "ostree_lib.import_gpg_key: missing key_path parameter" >&2
        return 1
    fi

    gpg --homedir="$(ostree_lib.get_ostree_gpg_homedir)" \
        --batch --yes \
        --import "${key_path}"
}

ostree_lib.gpg_signed_file_path() {
    local file="${1}"
    if [ -z "${file}" ]; then
        echo "ostree_lib.gpg_signed_file_path: missing file parameter" >&2
        return 1
    fi
    echo "${file}.asc"
}

ostree_lib.gpg_sign_file() {
    local file="${1}"
    if [ -z "${file}" ]; then
        echo "ostree_lib.gpg_sign_file: missing file parameter" >&2
        return 1
    fi
    if [ ! -f "${file}" ]; then
        echo "ostree_lib.gpg_sign_file: file ${file} does not exist" >&2
        return 1
    fi

    local homedir=
    homedir=$(ostree_lib.get_ostree_gpg_homedir)
    if [ -z "${homedir}" ]; then
        echo "ostree_lib.gpg_sign_file: cannot get homedir" >&2
        return 1
    fi

    local key_id=
    key_id=$(ostree_lib.get_ostree_gpg_key_id)
    if [ -z "${key_id}" ]; then
        echo "ostree_lib.gpg_sign_file: cannot get key_id" >&2
        return 1
    fi

    local asc_file=$(ostree_lib.gpg_signed_file_path "${file}")

    gpg --homedir "${homedir}" \
        --batch --yes \
        --local-user "${key_id}" \
        --armor \
        --detach-sign \
        --output "${asc_file}" \
        "${file}"
    echo "GPG signature file ${asc_file} created."
}

ostree_lib.patch_ostree_gpg_homedir() {
    local homedir="${MATRIXOS_OSTREE_DEV_GPG_HOMEDIR}"
    mkdir -p "${homedir}"
    chmod 700 "${homedir}"
    find "${homedir}" -type f -exec chmod 600 {} +
    chown -R root:root "${homedir}"
}

ostree_lib.list_remotes() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.list_remotes: missing ostree repodir parameter" >&2
        return 1
    fi
    ostree_lib.run_strict --repo="${repodir}" remote list
}

ostree_lib.maybe_initialize_gpg() {
    local gpg_enabled="${1}"
    if [ -z "${gpg_enabled}" ]; then
        return 0
    fi

    local remote="${2}"
    if [ -z "${remote}" ]; then
        echo "maybe_initialize_gpg: missing ostree remote parameter" >&2
        return 1
    fi

    local repodir="${3}"
    if [ -z "${repodir}" ]; then
        echo "maybe_initialize_gpg: missing ostree repodir parameter" >&2
        return 1
    fi

    local signing_pubkey=
    signing_pubkey=$(ostree_lib.get_gpg_pubkey_path)

    ostree_lib.import_gpg_key "${MATRIXOS_OSTREE_GPG_KEY_PATH}"
    ostree_lib.import_gpg_key "${signing_pubkey}"
    ostree_lib.import_gpg_key "${MATRIXOS_OSTREE_OFFICIAL_GPG_PUB_PATH}"
    ostree_lib.run --repo="${repodir}" remote gpg-import "${remote}" \
        -k "${MATRIXOS_OSTREE_GPG_KEY_PATH}" \
        -k "${signing_pubkey}" \
        -k "${MATRIXOS_OSTREE_OFFICIAL_GPG_PUB_PATH}"
}

ostree_lib.maybe_initialize_remote() {
    local remote="${1}"
    if [ -z "${remote}" ]; then
        echo "maybe_initialize_remote: missing ostree remote parameter" >&2
        return 1
    fi

    local remote_url="${2}"
    if [ -z "${remote_url}" ]; then
        echo "maybe_initialize_remote: missing ostree remote_url parameter" >&2
        return 1
    fi

    local gpg_enabled="${3}"  # can be empty.

    local repodir="${4}"
    if [ -z "${repodir}" ]; then
        echo "maybe_initialize_remote: missing ostree repodir parameter" >&2
        return 1
    fi

    if [ ! -d "${repodir}" ]; then
        mkdir -p "${repodir}"
    fi

    if [ ! -d "${repodir}/objects" ]; then
        echo "Initializing local ostree repo at ${repodir} ..."
        ostree_lib.run --repo="${repodir}" init --mode=archive
    else
        echo "ostree repo at ${repodir} already initialized. Reusing ..."
    fi

    local remotes=
    remotes=$( ostree_lib.list_remotes "${repodir}" )
    local remote_found=
    for r in "${remotes[@]}"; do
        if [ "${r}" = "${remote}" ]; then
            remote_found=1
            break
        fi
    done
    if [ -z "${remote_found}" ]; then
        echo "Initializing remote ${remote} at ${repodir} ..."
        ostree_lib.run --repo="${repodir}" remote add \
            $(ostree_lib.ostree_client_side_gpg_args "${gpg_enabled}") \
            "${remote}" "${remote_url}"
    else
        echo "Remote ${remote} already exists, reusing ..."
    fi
    echo "Showing current ostree remotes:"
    ostree_lib.run --repo="${repodir}" remote list -u
}

ostree_lib.pull() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.pull: missing ostree repodir parameter" >&2
        return 1
    fi

    local ref="${2}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.pull: missing ref parameter." >&2
        return 1
    fi

    # Trying to harmonize ostree pull with the rest of ostree tooling and embed remote
    # name inside ref here. Damn you ostree pull!
    local remote=
    remote=$(ostree_lib.extract_remote_from_ref "${ref}")
    if [ -z "${remote}" ]; then
        echo "ostree_lib.pull: ${ref} does not contain the remote: prefix (e.g. origin:)" >&2
        return 1
    fi
    ref=$(ostree_lib.clean_remote_from_ref "${ref}")

    echo "Pulling ostree from ${repodir} ${remote}:${ref} ..."
    ostree_lib.run --repo="${repodir}" pull "${remote}" "${ref}"
}

ostree_lib.deploy() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.deploy: missing ostree repodir parameter" >&2
        return 1
    fi
    shift

    local remote="${1}"
    if [ -z "${remote}" ]; then
        echo "ostree_lib.deploy: missing remote parameter" >&2
        return 1
    fi
    shift

    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.deploy: missing ref parameter" >&2
        return 1
    fi
    shift

    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.deploy: missing sysroot parameter" >&2
        return 1
    fi
    shift

    echo "Creating ${sysroot} ..."
    mkdir -p "${sysroot}"

    # Rest is always all the boot args.
    local boot_args=( "${@}" )

    local ostree_commit=
    ostree_commit=$(ostree_lib.last_commit "${repodir}" "${ref}")
    if [ -z "${ostree_commit}" ]; then
        echo "Cannot get last ostree commit" >&2
        return 1
    fi

    echo "Initializing ostree dir structure into ${sysroot} ..."
    ostree_lib.run admin init-fs "${sysroot}"

    echo "ostree os-init ..."
    ostree_lib.run admin os-init "${MATRIXOS_OSNAME}" --sysroot="${sysroot}"

    echo "ostree pull-local ..."
    ostree_lib.run pull-local --repo="${sysroot}/ostree/repo" "${repodir}" "${ostree_commit}"
    ostree_lib.run refs --repo="${sysroot}/ostree/repo" --create="${remote}:${ref}" "${ostree_commit}"

    echo "ostree setting bootloader to none (using blscfg instead) ..."
    ostree_lib.run config --repo="${sysroot}/ostree/repo" set sysroot.bootloader none

    echo "ostree setting bootprefix = false, given separate ${MATRIXOS_BOOT_ROOT} partition ..."
    ostree_lib.run config --repo="${sysroot}/ostree/repo" set sysroot.bootprefix false

    echo "ostree admin deploy ..."
    local ostree_boot_args=()
    for ba in "${boot_args[@]}"; do
        ostree_boot_args+=( "--karg-append=${ba}" )
    done
    ostree_lib.run admin deploy \
        --sysroot="${sysroot}" \
        --os="${MATRIXOS_OSNAME}" \
        "${ostree_boot_args[@]}" \
        "${remote}:${ref}"

    echo "ostree commit deployed: ${ostree_commit}."
}

ostree_lib.add_remote() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.add_remote: missing repodir parameter" >&2
        return 1
    fi
    local remote="${2}"
    if [ -z "${remote}" ]; then
        echo "ostree_lib.add_remote: missing ostree remote parameter" >&2
        return 1
    fi
    local remote_url="${3}"
    if [ -z "${remote_url}" ]; then
        echo "ostree_lib.add_remote: missing ostree remote_url parameter" >&2
        return 1
    fi
    local gpg_enabled="${4}"
    if [ -z "${gpg_enabled}" ]; then
        echo "ostree_lib.add_remote: missing ostree gpg_enabled parameter" >&2
        return 1
    fi

    ostree_lib.run remote add --repo="${repodir}" --force \
        $(ostree_lib.ostree_client_side_gpg_args "${gpg_enabled}") \
        "${remote}" "${remote_url}"
}

ostree_lib.add_remote_to_sysroot() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.add_remote_to_sysroot: missing sysroot parameter" >&2
        return 1
    fi
    local remote="${2}"
    if [ -z "${remote}" ]; then
        echo "ostree_lib.add_remote_to_sysroot: missing ostree remote parameter" >&2
        return 1
    fi
    local remote_url="${3}"
    if [ -z "${remote_url}" ]; then
        echo "ostree_lib.add_remote_to_sysroot: missing ostree remote_url parameter" >&2
        return 1
    fi
    local gpg_enabled="${4}"
    if [ -z "${gpg_enabled}" ]; then
        echo "ostree_lib.add_remote_to_sysroot: missing ostree gpg_enabled parameter" >&2
        return 1
    fi

    ostree_lib.run remote add --sysroot="${sysroot}" --force \
        $(ostree_lib.ostree_client_side_gpg_args "${gpg_enabled}") \
        "${remote}" "${remote_url}"
}

ostree_lib.last_commit() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.last_commit: missing repodir parameter." >&2
        return 1
    fi

    local ref="${2}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.last_commit: missing ref parameter." >&2
        return 1
    fi

    ostree_lib.run --repo="${repodir}" rev-parse "${ref}"
}

ostree_lib.last_commit_with_sysroot() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.last_commit_with_sysroot: missing sysroot parameter." >&2
        return 1
    fi
    local repodir="${sysroot%/}/ostree/repo"

    local ref="${2}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.last_commit_with_sysroot: missing ref parameter." >&2
        return 1
    fi

    ostree_lib.run --repo="${repodir}" rev-parse "${ref}"
}

ostree_lib.show_local_refs() {
    echo "Showing local ${repodir} ostree branches (refs):"
    ostree_lib.local_refs "${@}"
}

ostree_lib.local_refs() {
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.show_local_refs: missing ostree repodir parameter" >&2
        return 1
    fi
    ostree_lib.run --repo="${repodir}" refs
}

ostree_lib.show_remote_refs() {
    echo "Showing remote ostree branches (refs):"
    ostree_lib.remote_refs "${@}"
}

ostree_lib.remote_refs() {
    local remote="${1}"
    if [ -z "${remote}" ]; then
        echo "ostree_lib.show_remote_refs: missing ostree remote parameter" >&2
        return 1
    fi
    local repodir="${2}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.show_remote_refs: missing ostree repodir parameter" >&2
        return 1
    fi
    ostree_lib.run --repo="${repodir}" remote refs "${remote}"
}

ostree_lib.deployed_rootfs() {
    # This function works even before we call ostree deploy. Keep it that way.
    local repodir="${1}"
    if [ -z "${repodir}" ]; then
        echo "ostree_lib.deployed_rootfs: missing ostree repodir parameter" >&2
        return 1
    fi

    local ref="${2}"
    if [ -z "${ref}" ]; then
        echo "ostree_lib.deployed_rootfs: missing ref parameter" >&2
        return 1
    fi

    local sysroot="${3}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.deployed_rootfs: missing sysroot parameter" >&2
        return 1
    fi
    local ostree_commit=
    ostree_commit=$(ostree_lib.last_commit "${repodir}" "${ref}")
    if [ -z "${ostree_commit}" ]; then
        echo "Cannot get last ostree commit" >&2
        return 1
    fi
    local rootfs="${sysroot}/ostree/deploy/${MATRIXOS_OSNAME}/deploy/${ostree_commit}.0"
    echo "${rootfs}"
}

ostree_lib.booted_ref() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.booted_ref: missing ostree sysroot parameter" >&2
        return 1
    fi

    # This is terrible, but in golang we will be able to use --json.
    ostree_lib.run_strict --sysroot="${sysroot}" admin status | grep -A 2 '^\*' \
        | grep 'origin refspec' | awk '{print $3}'
}

ostree_lib.booted_hash() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.booted_hash: missing ostree sysroot parameter" >&2
        return 1
    fi

    # This is terrible, but in golang we will be able to use --json.
    ostree_lib.run_strict --sysroot="${sysroot}" admin status | \
        grep "* ${MATRIXOS_OSNAME}" | awk '{print $3}' | sed -E 's:\.[0-9]+::'
}

ostree_lib.list_packages() {
    local commit="${1}"
    if [ -z "${commit}" ]; then
        echo "ostree_lib.list_packages: missing commit parameter" >&2
        return 1
    fi

    local sysroot="${2}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.list_packages: missing sysroot parameter" >&2
        return 1
    fi

    local repodir="${sysroot%/}/ostree/repo"

    local vardbpkg="${sysroot%/}${MATRIXOS_RO_VDB}"
    local vdb="${MATRIXOS_RO_VDB}"
    if [ ! -d "${vardbpkg}" ]; then
        vardbpkg="${sysroot%/}/var/db/pkg"
        vdb="/var/db/pkg"
    fi
    if [ ! -d "${vardbpkg}" ]; then
        echo "ostree_lib.list_packages: ${vardbpkg} does not exist" >&2
        return 1
    fi

    # 1. ls -R the package database
    # 2. Filter for directories (Gentoo package entries)
    # 3. Extract path, strip prefix
    # 4. Filter for 'Category/Package' depth only
    ostree_lib.run_strict --repo="${repodir}" ls -R "${commit}" -- "${vdb}" \
        | grep "^d" \
        | awk '{print $5}' \
        | sed "s|^${vdb}/||" \
        | grep -E '^[^/]+/[^/]+$' \
        | sort
}

ostree_lib.upgrade() {
    local sysroot="${1}"
    if [ -z "${sysroot}" ]; then
        echo "ostree_lib.upgrade: missing ostree sysroot parameter" >&2
        return 1
    fi
    shift

    ostree_lib.run admin upgrade "${@}"
}
