#!/bin/bash
set -eu

source "${MATRIXOS_DEV_DIR:-/matrixos}"/headers/env.include.sh
source "${MATRIXOS_DEV_DIR}"/image/headers/imagerenv.include.sh

source "${MATRIXOS_DEV_DIR}"/lib/fs_lib.sh
source "${MATRIXOS_DEV_DIR}"/lib/ostree_lib.sh
source "${MATRIXOS_DEV_DIR}"/lib/qa_lib.sh
source "${MATRIXOS_DEV_DIR}"/image/lib/image_lib.sh
source "${MATRIXOS_DEV_DIR}"/image/lib/fsenc_lib.sh
source "${MATRIXOS_DEV_DIR}"/release/lib/release_lib.sh

ARG_POSITIONALS=()
ARG_PRODUCTIONIZE=
ARG_GPG_ENABLED="${MATRIXOS_OSTREE_GPG_ENABLED}"
ARG_CREATE_QCOW2=
ARG_USE_LOCAL_OSTREE=
ARG_OSTREE_REPODIR=
ARG_OSTREE_REMOTE=
ARG_OSTREE_REMOTE_URL=
ARG_OSTREE_REF=
ARG_WHOLE_DEVICE_PATH=
ARG_EFI_DEVICE_PATH=
ARG_BOOT_DEVICE_PATH=
ARG_ROOT_DEVICE_PATH=
ARG_USE_COMPRESSOR=

MOUNTS=()
LOOP_DEVICES=()
DEVICE_MAPPERS=()
TEMP_DIRS=()


umount_all() {
    fs_lib.cleanup_mounts "${MOUNTS[@]}"
    fs_lib.cleanup_cryptsetup_devices "${DEVICE_MAPPERS[@]}"
    fs_lib.cleanup_loop_devices "${LOOP_DEVICES[@]}"
}

clean_exit() {
    umount_all

    local tmpdir=
    for tmpdir in "${TEMP_DIRS[@]}"; do
        rmdir "${tmpdir}" || true  # ignore non-empty dirs.
    done
}

parse_args() {
    while [[ ${#} -gt 0 ]]; do
    case ${1} in
        -r|--ref|--ref=*)
        local val=
        if [[ "${1}" =~ --ref=.* ]]; then
            val=${1/--ref=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        if [ -z "${val}" ]; then
            echo "parse_args: invalid ref flag." >&2
            return 1
        fi

        if ostree_lib.is_branch_shortname "${val}"; then
            # assume dev to be safe.
            echo "${0}: WARNING: branch shortname specified, assuming dev release stage." >&2
            val=$(ostree_lib.branch_shortname_to_normal "dev" "${val}")
        fi
        ARG_OSTREE_REF="${val}"
        ;;

        -l|--local-ostree)
        ARG_USE_LOCAL_OSTREE=1

        shift
        ;;

        -prod|--productionize)
        ARG_PRODUCTIONIZE=1

        shift
        ;;

        -dgpg|--disable-gpg)
        ARG_GPG_ENABLED=""

        shift
        ;;

        -qcow2|--create-qcow2)
        ARG_CREATE_QCOW2=1

        shift
        ;;

        -comp|--compressor|--compressor=*)
        local val=
        if [[ "${1}" =~ --compressor=.* ]]; then
            val=${1/--compressor=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_USE_COMPRESSOR="${val}"
        ;;

        -or|--ostree-remote|--ostree-remote=*)
        local val=
        if [[ "${1}" =~ --ostree-remote=.* ]]; then
            val=${1/--ostree-remote=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_OSTREE_REMOTE="${val}"
        ;;

        -oru|--ostree-remote-url|--ostree-remote-url=*)
        local val=
        if [[ "${1}" =~ --ostree-remote-url=.* ]]; then
            val=${1/--ostree-remote-url=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_OSTREE_REMOTE_URL="${val}"
        ;;

        -repo|--ostree-repo|--ostree-repo=*)
        local val=
        if [[ "${1}" =~ --ostree-repo=.* ]]; then
            val=${1/--ostree-repo=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_OSTREE_REPODIR="${val}"
        ;;

        -instdev|--install-device|--install-device=*)
        local val=
        if [[ "${1}" =~ --install-device=.* ]]; then
            val=${1/--install-device=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_WHOLE_DEVICE_PATH="${val}"
        ;;

        -efidev|--efi-device-path|--efi-device-path=*)
        local val=
        if [[ "${1}" =~ --efi-device-path=.* ]]; then
            val=${1/--efi-device-path=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_EFI_DEVICE_PATH="${val}"
        ;;

        -bootdev|--boot-device-path|--boot-device-path=*)
        local val=
        if [[ "${1}" =~ --boot-device-path=.* ]]; then
            val=${1/--boot-device-path=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_BOOT_DEVICE_PATH="${val}"
        ;;

        -rootdev|--root-device-path|--root-device-path=*)
        local val=
        if [[ "${1}" =~ --root-device-path=.* ]]; then
            val=${1/--root-device-path=/}
            shift
        else
            val="${2}"
            shift 2
        fi
        ARG_ROOT_DEVICE_PATH="${val}"
        ;;

        -h|--help)
        echo -e "imager - matrixOS ostree to bootable image builder." >&2
        echo >&2
        echo -e "Arguments:" >&2
        echo -e "-r, --ref  \t\t\t\t\t the ostree ref name to build on (i.e. the name of the release branch, with or without remote)." >&2
        echo -e "-l, --local-ostree  \t\t\t\t use the local ostree repo instead of remote (or fetching from remote)." >&2
        echo -e "-qcow2, --create-qcow2  \t\t\t create a QCOW2 image too." >&2
        echo -e "  \t\t\t\t\t\t     default: ${MATRIXOS_LIVEOS_CREATE_QCOW2:-disabled}" >&2
        echo -e "-comp <xz|zstd|gz>, --compressor=<xz|zstd|gz>  \t compress the generated .img files using the given compressor." >&2
        echo -e "  \t\t\t\t\t\t     default: ${MATRIXOS_LIVEOS_IMAGES_COMPRESSOR}" >&2
        echo -e "-prod, --productionize  \t\t\t enable additional steps to generate a production ready image." >&2
        echo -e "  \t\t\t\t\t\t     Examples: generate sha256sums files, add GPG signatures, etc." >&2
        echo -e "-dgpg, --disable-gpg  \t\t\t\t force disable gpg support." >&2
        echo -e "-repo PATH, --ostree-repo=PATH  \t\t provide an alternative path to ostree repo." >&2
        echo -e "  \t\t\t\t\t\t     default: ${MATRIXOS_OSTREE_REPO_DIR}" >&2
        echo -e "-or <remote>, --ostree-remote=<remote>  \t provide an alternative name for the ostree remote." >&2
        echo -e "  \t\t\t\t\t\t     default: ${MATRIXOS_OSTREE_REMOTE}" >&2
        echo -e "-oru <url>, --ostree-remote-url=<url>  \t\t provide a URL to use for the ostree remote." >&2
        echo -e "  \t\t\t\t\t\t     default: ${MATRIXOS_OSTREE_REMOTE_URL}" >&2

        echo -e "-instdev <device>, --install-device=<device>  \t provide an alternative whole block device path (e.g. /dev/sda) for imaging (installation)." >&2
        echo -e "-efidev <device>, --efi-device-path=<device>  \t provide an alternative EFI System Partition path (will not be formatted)." >&2
        echo -e "-bootdev <device>, --boot-device-path=<device>   format and install /boot into the specified device (DATA WIPED)." >&2
        echo -e "-rootdev <device>, --root-device-path=<device>   format and install / into the specified device (DATA WIPED)." >&2

        echo >&2
        exit 0
        ;;

        -*|--*)
        echo "Unknown argument ${1}" >&2
        return 1
        ;;

        *)
        ARG_POSITIONALS+=( "${1}" )
        shift
        ;;
    esac
    done
}

setup_image() {
    local remote="${1}"
    if [ -z "${remote}" ]; then
        echo "setup_image: missing ostree remote parameter" >&2
        return 1
    fi

    local remote_url="${2}"
    if [ -z "${remote_url}" ]; then
        echo "setup_image: missing ostree remote_url parameter" >&2
        return 1
    fi

    local repodir="${3}"
    if [ -z "${repodir}" ]; then
        echo "setup_image: missing ostree repodir parameter" >&2
        return 1
    fi

    local ref="${4}"
    if [ -z "${ref}" ]; then
        echo "setup_image: missing ref parameter" >&2
        return 1
    fi

    local whole_device="${5}"  # can be empty. already validated.
    local efi_device="${6}"  # can be empty. already validated.
    local boot_device="${7}"  # can be empty. already validated.
    local root_device="${8}"  # can be empty. already validated.
    local productionize="${9}"  # can be empty.
    local gpg_enabled="${10}"  # can be empty.
    local create_qcow2="${11}"  # can be empty.
    local compressor="${12}"  # can be empty.
    local encryption_enabled="${13}"  # can be empty.

    local mount_rootfs
    mount_rootfs=$(fs_lib.create_temp_dir "${MATRIXOS_IMAGES_MOUNT_DIR}" "rootfs")
    TEMP_DIRS+=( "${mount_rootfs}" )

    # Determine if we need to write an image file or write to a specific device instead.
    local deploy_ondev=
    if [ -n "${boot_device}" ] || [ -n "${root_device}" ] || [ -n "${efi_device}" ]; then
        # Note: main() already checked for the correctness of the params and has validated
        # that we have all 3 or nothing. But we will now check again to make sure.
        for d in "${boot_device}" "${root_device}" "${efi_device}"; do
            if [ -z "${d}" ]; then
                echo "Failsafe: missing boot device parameter from input! Aborting hard." >&2
                return 1
            fi
        done
        deploy_ondev=1
    fi

    local block_device
    if [ -n "${whole_device}" ]; then
        image_lib.clear_partition_table "${whole_device}"
        udevadm settle &>/dev/null

        image_lib.partition_devices \
            "${MATRIXOS_LIVEOS_EFI_SIZE}" \
            "${MATRIXOS_LIVEOS_BOOT_SIZE}" \
            "${MATRIXOS_LIVEOS_IMAGE_SIZE}" \
            "${whole_device}"

        udevadm settle &>/dev/null

        local part_efifs=
        local part_bootfs=
        local part_rootfs=
        part_efifs=$(image_lib.block_device_nth_partition_path "${whole_device}" 1)
        if [ -z "${part_efifs}" ]; then
            echo "Unable to get partition 1 of ${whole_device}" >&2
            return 1
        fi
        part_bootfs=$(image_lib.block_device_nth_partition_path "${whole_device}" 2)
        if [ -z "${part_bootfs}" ]; then
            echo "Unable to get partition 2 of ${whole_device}" >&2
            return 1
        fi
        part_rootfs=$(image_lib.block_device_nth_partition_path "${whole_device}" 3)
        if [ -z "${part_rootfs}" ]; then
            echo "Unable to get partition 3 of ${whole_device}" >&2
            return 1
        fi
        for lp in "${part_efifs}" "${part_bootfs}" "${part_rootfs}"; do
            if [ ! -e "${lp}" ]; then
                echo "${lp} does not exist." >&2
                return 1
            fi
        done

        image_lib.format_efifs "${part_efifs}"

        # overwrite efi_device at this point.
        efi_device="${part_efifs}"
        boot_device="${part_bootfs}"
        root_device="${part_rootfs}"
        block_device="${whole_device}"

    elif [ -z "${deploy_ondev}" ]; then

        local image_path
        image_path=$(image_lib.image_path "${ref}")
        image_lib.create_image "${image_path}" "${MATRIXOS_LIVEOS_IMAGE_SIZE}"

        image_lib.partition_devices \
            "${MATRIXOS_LIVEOS_EFI_SIZE}" \
            "${MATRIXOS_LIVEOS_BOOT_SIZE}" \
            "${MATRIXOS_LIVEOS_IMAGE_SIZE}" \
            "${image_path}"

        block_device=$(fsenc_lib.mount_image_as_loop_device "${image_path}")
        if [ -z "${block_device}" ]; then
            echo "Unable to mount loop device" >&2
            return 1
        fi
        LOOP_DEVICES+=( "${block_device}" )

        # Wait for the loop device to be really ready.
        udevadm settle &>/dev/null

        local loop_part_efifs="${block_device}p1"
        local loop_part_bootfs="${block_device}p2"
        local loop_part_rootfs="${block_device}p3"
        local lp=
        for lp in "${loop_part_efifs}" "${loop_part_bootfs}" "${loop_part_rootfs}"; do
            if [ ! -e "${lp}" ]; then
                echo "${lp} does not exist." >&2
                return 1
            fi
        done

        image_lib.format_efifs "${loop_part_efifs}"

        # overwrite efi_device at this point.
        efi_device="${loop_part_efifs}"
        boot_device="${loop_part_bootfs}"
        root_device="${loop_part_rootfs}"
    elif [ -n "${deploy_ondev}" ]; then
        echo "EFI System Partition at ${efi_device} will NOT be formatted. (yay?)"
        echo "Boot device ${boot_device} will be used to derive parent block device, and install bootloader."
        block_device=$(image_lib.block_device_for_partition_path "${boot_device}")
    else
        echo "Unsupported setup mode." >&2
        return 1
    fi

    # We may not be formatting the EFI device, so let's use the uuid instead of label
    # for the bootloader config.
    local efi_device_uuid
    efi_device_uuid=$(fs_lib.device_uuid "${efi_device}")
    if [ -z "${efi_device_uuid}" ]; then
        echo "Unable to get UUID for ${efi_device}" >&2
        return 1
    fi

    image_lib.format_bootfs "${boot_device}"
    # Used for the bootloader instead of relying on label.
    local boot_device_uuid
    boot_device_uuid=$(fs_lib.device_uuid "${boot_device}")
    if [ -z "${boot_device_uuid}" ]; then
        echo "Unable to get UUID for ${boot_device}" >&2
        return 1
    fi

    # Save the physical loop partition device node path, so that we can get
    # real underlying UUID.
    local physical_root_device="${root_device}"
    if [ -n "${encryption_enabled}" ]; then
        local luks_device=
        luks_device=$(fs_lib.get_luks_rootfs_device_path "${MATRIXOS_LIVEOS_ENCRYPTION_ROOTFS_NAME}")
        fsenc_lib.luks_encrypt "${root_device}" "${luks_device}" "DEVICE_MAPPERS"
        # Now we trick the script and rewrite root_device= to point to the devmapper device.
        root_device="${luks_device}"
        echo "New encrypted rootfs partition: ${root_device}"
    fi

    image_lib.format_rootfs "${root_device}"
    local root_device_uuid
    root_device_uuid=$(fs_lib.device_uuid "${root_device}")
    if [ -z "${root_device_uuid}" ]; then
        echo "Unable to get UUID for ${root_device}" >&2
        return 1
    fi

    image_lib.mount_rootfs "${root_device}" "${mount_rootfs}"
    MOUNTS+=( "${mount_rootfs}" )

    local mount_efifs="${mount_rootfs}${MATRIXOS_EFI_ROOT}"
    image_lib.mount_efifs "${efi_device}" "${mount_efifs}"
    MOUNTS+=( "${mount_efifs}" )

    local mount_bootfs="${mount_rootfs}${MATRIXOS_BOOT_ROOT}"
    image_lib.mount_bootfs "${boot_device}" "${mount_bootfs}"
    MOUNTS+=( "${mount_bootfs}" )

    # Backup luks header only now that we have all devices mounted.
    if [ -n "${encryption_enabled}" ]; then
        fsenc_lib.luks_backup_header "${root_device}" "${mount_efifs}"
    fi

    local kernel_boot_args=()
    image_lib.generate_kernel_boot_args "kernel_boot_args" "${ref}" "${efi_device}" "${boot_device}" \
        "${physical_root_device}" "${root_device}" "${encryption_enabled}"

    local boot_args=( "${kernel_boot_args[@]}" )
    boot_args+=(
        "root=UUID=${root_device_uuid}"
        rw
        splash
        quiet
    )
    echo "Boot arguments: ${boot_args[@]}"

    local efiboot_path
    efiboot_path=$(dirname "${MATRIXOS_GRUB_CFG_RELATIVE_EFI_PATH}")
    local efibootdir="${mount_efifs}${efiboot_path}"

    echo "Deploying ostree into ${mount_rootfs} ..."
    # Set the remote locally forcefully to avoid getting imager confused
    ostree_lib.add_remote "${repodir}" "${remote}" "${remote_url}" "${gpg_enabled}"
    ostree_lib.deploy "${repodir}" "${remote}" "${ref}" "${mount_rootfs}" "${boot_args[@]}"
    # set up remote for clients using the image.
    ostree_lib.add_remote_to_sysroot "${mount_rootfs}" "${remote}" "${remote_url}" "${gpg_enabled}"

    local rootfs
    rootfs=$(ostree_lib.deployed_rootfs "${repodir}" "${ref}" "${mount_rootfs}")

    qa_lib.verify_distro_rootfs_environment_setup "${rootfs}"
    image_lib.setup_bootloader_config "${ref}" "${rootfs}" "${mount_rootfs}" "${mount_bootfs}" "${efibootdir}" \
        "${efi_device_uuid}" "${boot_device_uuid}"
    image_lib.setup_passwords "${rootfs}"

    image_lib.install_bootloader "MOUNTS" "${rootfs}" "${mount_efifs}" "${mount_bootfs}" \
        "${block_device}" "${efibootdir}"

    image_lib.install_secureboot_certs "${rootfs}" "${mount_efifs}" "${efibootdir}"
    image_lib.install_memtest "${rootfs}" "${efibootdir}"
    local pkglist=()
    image_lib.package_list "pkglist" "${rootfs}"
    image_lib.finalize_filesystems "${mount_rootfs}" "${mount_bootfs}" "${mount_efifs}"
    image_lib.show_final_filesystem_info "${block_device}" "${mount_bootfs}" "${mount_efifs}"

    local release_version=
    release_version=$(image_lib.release_version "${rootfs}")
    umount_all
    sync

    if [ -n "${image_path}" ]; then
        local generated_artifacts=()
        if [ -n "${productionize}" ]; then
            echo "Productionizing ${image_path} for release version: ${release_version} ..."
            local new_image_path=
            _productionize_image "${release_version}" "${image_path}" "${ref}" \
                "${productionize}" "${gpg_enabled}" "${create_qcow2}" "new_image_path" \
                "pkglist" "generated_artifacts"
            echo "Final image path: ${new_image_path}"
            image_path="${new_image_path}"
        fi

        image_lib.show_test_info "${generated_artifacts[@]}"
        echo "Image creation complete! > ${image_path}"
    else
        sync
        echo "On device install complete!"
    fi
}

_productionize_image() {
    local release_version="${1}"
    if [ -z "${release_version}" ]; then
        echo "_productionize_image: missing release_version parameter" >&2
        return 1
    fi

    local image_path="${2}"
    if [ -z "${image_path}" ]; then
        echo "_productionize_image: missing image_path parameter" >&2
        return 1
    fi

    local ref="${3}"
    if [ -z "${ref}" ]; then
        echo "_productionize_image: missing ref parameter" >&2
        return 1
    fi

    local productionize="${4}"  # can be empty.
    local gpg_enabled="${5}"  # can be empty.
    local create_qcow2="${6}"  # can be empty.

    local -n __new_image_path="${7}"
    local -n __pkg_list="${8}"
    local -n __generated_artifacts="${9}"

    local versioned_image_path
    versioned_image_path=$(image_lib.image_path_with_release_version "${ref}" "${release_version}")
    echo "Moving ${image_path} to ${versioned_image_path} ..."
    mv "${image_path}" "${versioned_image_path}"
    image_path="${versioned_image_path}"
    chmod 644 "${image_path}"

    local qcow2_image_path=
    local qcow2_image_name=
    local qcow2_image_dir=
    if [ -n "${create_qcow2}" ]; then
        echo "Creating QCOW2 image for ${image_path} ..."
        image_lib.create_qcow2_image "${image_path}"
        qcow2_image_path=$(image_lib.qcow2_image_path "${image_path}")
        qcow2_image_name=$(basename "${qcow2_image_path}")
        qcow2_image_dir=$(dirname "${qcow2_image_path}")
        __generated_artifacts+=( "${qcow2_image_path}" )
    fi

    # create package list file
    local pkglist_path="${image_path}.packages.txt"
    echo "Creating package list file: ${pkglist_path}"
    echo > "${pkglist_path}"
    for pkg in "${__pkg_list[@]}"
    do
        echo "${pkg}" >> "${pkglist_path}"
    done
    __generated_artifacts+=( "${pkglist_path}" )

    if [ -n "${compressor}" ]; then
        echo "Compressing the image using: ${compressor}"
        image_lib.compress_image "${image_path}" "${compressor}"
        image_path=$(image_lib.image_path_with_compressor_extension "${image_path}" "${compressor}")
        echo "Image compressed, new image path: ${image_path}"
    fi
    __generated_artifacts+=( "${image_path}" )

    if [[ -n "${productionize}" ]]; then
        echo "Creating sha256sum of: ${image_path}"
        local image_dir
        image_dir=$(dirname "${image_path}")
        local image_name
        image_name=$(basename "${image_path}")
        local sha256_path="${image_dir}/${image_name}.sha256"
        local qcow2_sha256_path="${qcow2_image_dir}/${qcow2_image_name}.sha256"

        pushd "${image_dir}" >/dev/null
        sha256sum "${image_name}" > "${sha256_path}"
        popd >/dev/null
        if [ -n "${create_qcow2}" ]; then
            echo "Creating sha256sum of: ${qcow2_image_path}"
            pushd "${qcow2_image_dir}" >/dev/null
            sha256sum "${qcow2_image_name}" > "${qcow2_sha256_path}"
            popd >/dev/null
            __generated_artifacts+=( "${qcow2_sha256_path}" )
        fi
        __generated_artifacts+=( "${sha256_path}" )

        local mos_gpg_key="${MATRIXOS_OSTREE_GPG_KEY_PATH}"
        if [ -z "${gpg_enabled}" ]; then
            echo "WARNING: GPG signing of images not enabled in settings." >&2
        elif [ -f "${mos_gpg_key}" ]; then
            echo "${mos_gpg_key} exists, creating GPG signatures ..."
            release_lib.initialize_gpg "${gpg_enabled}"
            ostree_lib.gpg_sign_file "${image_path}"
            __generated_artifacts+=( "$(ostree_lib.gpg_signed_file_path "${image_path}")" )
            if [ -n "${create_qcow2}" ]; then
                echo "Creating GPG signatures of: ${qcow2_image_path}"
                ostree_lib.gpg_sign_file "${qcow2_image_path}"
                __generated_artifacts+=( "$(ostree_lib.gpg_signed_file_path "${qcow2_image_path}")" )
            fi
        else
            echo "WARNING: ${mos_gpg_key} not found. Cannot create GPG signatures of image." >&2
        fi
    fi

    __new_image_path="${image_path}"
}

main() {
    trap clean_exit EXIT

    parse_args "${@}"
    qa_lib.root_privs

    imager_env.validate_luks_variables

    if [ -z "${ARG_OSTREE_REF}" ]; then
        echo "--ref= unset. Unable to proceed." >&2
        return 1
    fi
    local ref="${ARG_OSTREE_REF}"

    local remote=
    if [ -n "${ARG_OSTREE_REMOTE}" ]; then
        remote="${ARG_OSTREE_REMOTE}"
    else
        echo "--ostree-remote= unset. Using default --ostree-remote=${MATRIXOS_OSTREE_REMOTE}." >&2
        remote="${MATRIXOS_OSTREE_REMOTE}"
    fi

    local remote_url=
    if [ -n "${ARG_OSTREE_REMOTE_URL}" ]; then
        remote_url="${ARG_OSTREE_REMOTE_URL}"
    else
        echo "--ostree-remote-url= unset. Using default --ostree-remote-url=${MATRIXOS_OSTREE_REMOTE_URL}." >&2
        remote_url="${MATRIXOS_OSTREE_REMOTE_URL}"
    fi

    local repodir=
    if [ -n "${ARG_OSTREE_REPODIR}" ]; then
        repodir="${ARG_OSTREE_REPODIR}"
    else
        echo "--ostree-repo= unset. Using default --ostree-repo=${MATRIXOS_OSTREE_REPO_DIR}." >&2
        repodir="${MATRIXOS_OSTREE_REPO_DIR}"
    fi

    local create_qcow2=
    if [ -n "${ARG_CREATE_QCOW2}" ]; then
        create_qcow2="${ARG_CREATE_QCOW2}"
    else
        echo "--create-qcow2 unset. Using default --create-qcow2=${MATRIXOS_LIVEOS_CREATE_QCOW2}." >&2
        create_qcow2="${MATRIXOS_LIVEOS_CREATE_QCOW2}"
    fi

    local compressor=
    if [ -n "${ARG_USE_COMPRESSOR}" ]; then
        compressor="${ARG_USE_COMPRESSOR}"
    else
        echo "--compressor= unset. Using default --compressor=${MATRIXOS_LIVEOS_IMAGES_COMPRESSOR}." >&2
        compressor="${MATRIXOS_LIVEOS_IMAGES_COMPRESSOR}"
    fi

    local efi_device=
    if [ -n "${ARG_EFI_DEVICE_PATH}" ]; then
        efi_device="${ARG_EFI_DEVICE_PATH}"
        if [ ! -e "${efi_device}" ]; then
            echo "${efi_device} does not exist ..." >&2
            return 1
        fi
        echo "Selected the following device as EFI System Partition: ${efi_device} (WILL NOT BE FORMATTED)"
        blkid "${efi_device}"
    fi

    local boot_device=
    if [ -n "${ARG_BOOT_DEVICE_PATH}" ]; then
        boot_device="${ARG_BOOT_DEVICE_PATH}"
        if [ ! -e "${boot_device}" ]; then
            echo "${boot_device} does not exist ..." >&2
            return 1
        fi
    fi
    local root_device=
    if [ -n "${ARG_ROOT_DEVICE_PATH}" ]; then
        root_device="${ARG_ROOT_DEVICE_PATH}"
        if [ ! -e "${root_device}" ]; then
            echo "${root_device} does not exist ..." >&2
            return 1
        fi
    fi
    local whole_device=
    if [ -n "${ARG_WHOLE_DEVICE_PATH}" ]; then
        whole_device="${ARG_WHOLE_DEVICE_PATH}"
        if [ ! -e "${whole_device}" ]; then
            echo "${whole_device} does not exist ..." >&2
            return 1
        fi
    fi

    local d=
    local any_device=
    for d in "${boot_device}" "${root_device}" "${efi_device}"; do
        if [ -n "${d}" ]; then
            any_device=1
        fi
    done
    if [ -n "${any_device}" ]; then
        local every_set=1
        for d in "${boot_device}" "${root_device}" "${efi_device}"; do
            if [ -z "${d}" ]; then
                every_set=
            fi
        done
        if [ -z "${every_set}" ]; then
            echo "Please specify all the --*-device-path= flags." >&2
            return 1
        fi
    fi
    if [ -n "${whole_device}" ] && [ -z "${any_device}" ]; then
        echo "Specified whole device ${whole_device} to flash." >&2
    elif [ -n "${whole_device}" ] && [ -n "${any_device}" ]; then
        echo "Please specify either --install-device=* or all of the individual device partition paths. Not both." >&2
        return 1
    fi

    local gpg_enabled="${ARG_GPG_ENABLED}"

    qa_lib.verify_imager_environment_setup "/" "${gpg_enabled}"
    if [ -n "${ARG_USE_LOCAL_OSTREE}" ]; then
        ostree_lib.show_local_refs "${repodir}"
        ostree_lib.maybe_initialize_gpg "${gpg_enabled}" "${remote}" "${repodir}"
    else
        # check if we have the remote inside the ref.
        local remoted_ref
        remoted_ref=$(ostree_lib.extract_remote_from_ref "${ref}")
        if [ -n "${remoted_ref}" ]; then
            remote="${remoted_ref}"
            ref=$(ostree_lib.clean_remote_from_ref "${ref}")
            echo "WARNING: ${ref} contains the remote reference, using remote=${remoted_ref} and ref=${ref}" >&2
        fi
        ostree_lib.maybe_initialize_remote "${remote}" "${remote_url}" "${gpg_enabled}" "${repodir}"
        ostree_lib.maybe_initialize_gpg "${gpg_enabled}" "${remote}" "${repodir}"
        ostree_lib.show_remote_refs "${remote}" "${repodir}"
        ostree_lib.pull "${repodir}" "${remote}:${ref}"
    fi
    setup_image "${remote}" "${remote_url}" "${repodir}" "${ref}" \
        "${whole_device}" "${efi_device}" "${boot_device}" "${root_device}" \
        "${ARG_PRODUCTIONIZE}" "${gpg_enabled}" \
        "${create_qcow2}" "${compressor}" "${MATRIXOS_LIVEOS_ENCRYPTION}"
}

main "${@}"
