#!/bin/bash
# matrixOS images manipulation library.
set -e

source "${MATRIXOS_DEV_DIR:-/matrixos}/headers/env.include.sh"
source "${MATRIXOS_DEV_DIR}"/image/headers/imagerenv.include.sh

source "${MATRIXOS_DEV_DIR}"/lib/fs_lib.sh
source "${MATRIXOS_DEV_DIR}"/lib/ostree_lib.sh


image_lib.release_version() {
    local rootfs="${1}"
    if [ -z "${rootfs}" ]; then
        echo "image_lib.release_version: missing rootfs parameter" >&2
        return 1
    fi

    local release_version
    release_version=$(date +%Y%m%d)
    local metadata_file="${rootfs}${MATRIXOS_SEEDER_BUILD_METADATA_FILE}"
    if [ -f "${metadata_file}" ]; then
        echo "Build metadata:" >&2
        cat "${metadata_file}" >&2
        # extract version from SEED_NAME= if available.
        local seed_name
        seed_name=$(cat "${metadata_file}" | grep 'SEED_NAME=' | sed 's:SEED_NAME=::')
        if [ -n "${seed_name}" ]; then
            release_version="${seed_name##*-}"
            echo "Extracted release version: ${release_version}" >&2
        else
            echo "WARNING: SEED_NAME= not found in ${metadata_file}" >&2
        fi
    else
        echo "WARNING! Build metadata file not found: ${metadata_file}" >&2
    fi

    echo "${release_version}"
}

_image_path() {
    local suffix="${1}"
    local image_path="${MATRIXOS_IMAGES_OUT_DIR}/${suffix}"
    echo ${image_path}
}

image_lib.image_path() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "image_lib.image_path: missing ref parameter" >&2
        return 1
    fi

    # Clean remote like "origin:"
    ref=$(ostree_lib.clean_remote_from_ref "${ref}")

    local suffix="${ref//\//_}.img"
    _image_path "${suffix}"
}

image_lib.image_path_with_release_version() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "image_lib.image_path_with_release_version: missing ref parameter" >&2
        return 1
    fi
    local release_version="${2}"
    if [ -z "${release_version}" ]; then
        echo "image_lib.image_path_with_release_version: missing release_version parameter" >&2
        return 1
    fi

    # Clean remote like "origin:"
    ref=$(ostree_lib.clean_remote_from_ref "${ref}")

    local suffix="${ref//\//_}-${release_version}.img"
    _image_path "${suffix}"
}

image_lib.create_image() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "Unable to produce image path" >&2
        return 1
    fi
    local image_size="${2}"
    if [ -z "${image_size}" ]; then
        echo "image_lib.create_image: missing image_size parameter" >&2
        return 1
    fi

    local imagesdir=
    imagesdir=$(dirname "${image_path}")
    echo "Creating images directory: ${imagesdir} (if it does not exist)"
    mkdir -p "${imagesdir}"

    # Don't skip this or sgdisk gets confused due to truncate.
    image_lib.remove_image_file "${image_path}"

    echo "Creating block device image file: ${image_path}"
    truncate -s "${image_size}" "${image_path}"
}

image_lib.image_path_with_compressor_extension() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "image_lib.image_path_with_compressor_extension: missing image_path parameter" >&2
        return 1
    fi
    local compressor="${2}"
    if [ -z "${compressor}" ]; then
        echo "image_lib.image_path_with_compressor_extension: missing compressor parameter" >&2
        return 1
    fi
    local comp=
    read -ra comp <<< "${compressor}"

    echo "${image_path}.${comp[0]}"
}

image_lib.compress_image() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "image_lib.compress_image: missing image_path parameter" >&2
        return 1
    fi
    local compressor="${2}"
    if [ -z "${compressor}" ]; then
        echo "image_lib.compress_image: missing compressor parameter" >&2
        return 1
    fi

    local image_path_with_ext=
    image_path_with_ext=$(image_lib.image_path_with_compressor_extension "${image_path}" "${compressor}")

    local comp=
    read -ra comp <<< "${compressor}"
    "${comp[@]}" "${image_path}"

    if [ ! -f "${image_path_with_ext}" ]; then
        echo "image_lib.compress_image: image_path was not created with the expected extension" >&2
        return 1
    fi
}

image_lib.block_device_nth_partition_path() {
    local block_device="${1}"
    if [ -z "${block_device}" ]; then
        echo "image_lib.get_block_device_nth_partition_path: missing block_device parameter" >&2
        return 1
    fi
    local nth="${2}"
    if [ -z "${nth}" ]; then
        echo "image_lib.get_block_device_nth_partition_path: missing nth parameter" >&2
        return 1
    fi

    lsblk -nr -o PATH,PARTN "${block_device}" | awk -v "n=${nth}" '$2 == n {print $1; exit}'
}

image_lib.block_device_for_partition_path() {
    local partition_path="${1}"
    if [ -z "${partition_path}" ]; then
        echo "image_lib.block_device_for_partition_path: missing partition_path parameter" >&2
        return 1
    fi

    lsblk -no PKNAME -p "${partition_path}"
}

image_lib.setup_bootloader_config() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "image_lib.setup_bootloader_config: missing ref parameter" >&2
        return 1
    fi

    ref=$(ostree_lib.clean_remote_from_ref "${ref}")
    ref=$(ostree_lib.remove_full_from_branch "${ref}")
    if [ -z "${ref}" ]; then
        echo "image_lib.setup_bootloader_config: invalid ref parameter" >&2
        return 1
    fi

    local ostree_deploy_rootfs="${2}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "image_lib.setup_bootloader_config: missing ostree ostree_deploy_rootfs parameter" >&2
        return 1
    fi

    local sysroot="${3}"
    if [ -z "${sysroot}" ]; then
        echo "image_lib.setup_bootloader_config: missing sysroot parameter" >&2
        return 1
    fi

    local bootdir="${4}"
    if [ -z "${bootdir}" ]; then
        echo "image_lib.setup_bootloader_config: missing bootdir parameter" >&2
        return 1
    fi

    local efibootdir="${5}"
    if [ -z "${efibootdir}" ]; then
        echo "image_lib.setup_bootloader_config: missing efibootdir parameter" >&2
        return 1
    fi

    local efi_uuid="${6}"
    if [ -z "${efi_uuid}" ]; then
        echo "image_lib.setup_bootloader_config: missing efi_uuid parameter" >&2
        return 1
    fi
    local boot_uuid="${7}"
    if [ -z "${boot_uuid}" ]; then
        echo "image_lib.setup_bootloader_config: missing boot_uuid parameter" >&2
        return 1
    fi

    local kernel_ver=
    kernel_ver=$(image_lib.get_kernel_path "${ostree_deploy_rootfs}")
    if [ -z "${kernel_ver}" ]; then
        echo "No kernel dir in ${modulesdir}/" >&2
        return 1
    fi

    local ostree_boot_commit=
    ostree_boot_commit=$(ostree_lib.boot_commit "${sysroot}")
    if [ -z "${ostree_boot_commit}" ]; then
        echo "Cannot determine ostree boot commit." >&2
        return 1
    fi
    echo "Found boot commit: ${ostree_boot_commit}"

    local boot_deploy_dir="${MATRIXOS_DEV_DIR}/image/boot/${ref}"
    local src_grubcfg_path="${boot_deploy_dir}/grub.cfg"

    # This can be called before grub-install, so make sure to have the dir.
    mkdir -p "${efibootdir}"

    local dst_grubcfg_path="${efibootdir}/grub.cfg"
    echo "Copying grub: ${src_grubcfg_path} -> ${dst_grubcfg_path}"
    cp -v "${src_grubcfg_path}" "${dst_grubcfg_path}"

    local themesdir="${ostree_deploy_rootfs}"/usr/share/grub/themes/"${MATRIXOS_OSNAME}-theme"
    if [ -d "${themesdir}" ]; then
        echo "Copying GRUB themes from ${themesdir} ..."
        mkdir -p "${bootdir}"/grub/themes
        cp -v -rp "${themesdir}" "${bootdir}"/grub/themes/
    fi

    # Set GRUB_CFG to /efi/efi/BOOT/grub.cfg to make everyone happy and aware of where
    # the current grub.cfg should sit.
    echo "GRUB_CFG=${MATRIXOS_GRUB_CFG_EFI_PATH}" > \
        "${ostree_deploy_rootfs}/etc/environment.d/99-matrixos-imager-grub.conf"

    # Set up BOOTUUID.
    sed -i "s:%BOOTUUID%:${boot_uuid}:g" "${dst_grubcfg_path}"
    # Set up EFIUUID.
    sed -i "s:%EFIUUID%:${efi_uuid}:g" "${dst_grubcfg_path}"
    # Set up OSNAME.
    sed -i "s:%OSNAME%:${MATRIXOS_OSNAME}:g" "${dst_grubcfg_path}"

    echo "Current grub.cfg:"
    cat "${dst_grubcfg_path}"
    echo "EOF"
}

image_lib.install_bootloader() {
    if [ -z "${1}" ]; then
        echo "image_lib.install_bootloader: missing array parameter" >&2
        return 1
    fi
    local -n _bootloader_mounts="${1}"

    local ostree_deploy_rootfs="${2}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "image_lib.install_bootloader: missing ostree_deploy_rootfs parameter" >&2
        return 1
    fi

    local mount_efifs="${3}"
    if [ -z "${mount_efifs}" ]; then
        echo "image_lib.install_bootloader: missing mount_efifs parameter" >&2
        return 1
    fi

    local mount_bootfs="${4}"
    if [ -z "${mount_bootfs}" ]; then
        echo "image_lib.install_bootloader: missing mount_bootfs parameter" >&2
        return 1
    fi

    local block_device="${5}"
    if [ -z "${block_device}" ]; then
        echo "image_lib.install_bootloader: missing block_device parameter" >&2
        return 1
    fi

    local efibootdir="${6}"
    if [ -z "${efibootdir}" ]; then
        echo "image_lib.install_bootloader: missing efibootdir parameter" >&2
        return 1
    fi
    echo "Installing bootloader ..."

    local efi_chroot_mount="${ostree_deploy_rootfs}${MATRIXOS_EFI_ROOT}"
    fs_lib.bind_mount "${!_bootloader_mounts}" "${mount_efifs}" "${efi_chroot_mount}"

    local boot_chroot_mount="${ostree_deploy_rootfs}${MATRIXOS_BOOT_ROOT}"
    fs_lib.bind_mount "${!_bootloader_mounts}" "${mount_bootfs}" "${boot_chroot_mount}"

    fs_lib.setup_common_rootfs_mounts "${!_bootloader_mounts}" "${ostree_deploy_rootfs}"

    # MATRIXOS_EFI_ROOT mounted inside the chroot at /efi
    # MATRIXOS_BOOT_ROOT mounted inside the chroot at /boot
    chroot "${ostree_deploy_rootfs}" \
        /usr/bin/grub-install \
        --target=x86_64-efi \
        --directory="/usr/lib/grub/x86_64-efi" \
        --efi-directory="${MATRIXOS_EFI_ROOT}" \
        --boot-directory="${MATRIXOS_BOOT_ROOT}" \
        --themes="${MATRIXOS_OSNAME}-theme" \
        --removable \
        --modules="ext2 btrfs gzio part_gpt fat part_msdos all_video" \
        "${block_device}"

    fs_lib.bind_umount "${boot_chroot_mount}"
    fs_lib.bind_umount "${efi_chroot_mount}"
    fs_lib.unsetup_common_rootfs_mounts "${ostree_deploy_rootfs}"

    # We expect grub to have created efi/BOOT/BOOTX64.EFI
    bootx64efi="${efibootdir}/BOOTX64.EFI"
    if [ ! -e "${bootx64efi}" ]; then
        echo "${bootx64efi} does not exist." >&2
        return 1
    fi

    local grubx64efi="${efibootdir}/GRUBX64.EFI"
    echo "Removing existing ${grubx64efi} as it's not signed ..."
    rm -f "${grubx64efi}"
    local signed_grubx64efi="${ostree_deploy_rootfs}/usr/lib/grub/grub-x86_64.efi.signed"
    echo "Moving ${signed_grubx64efi} to ${grubx64efi}"
    mv "${signed_grubx64efi}" "${grubx64efi}"
}

image_lib.install_secureboot_certs() {
    local ostree_deploy_rootfs="${1}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "image_lib.install_secureboot_certs: missing ostree_deploy_rootfs parameter" >&2
        return 1
    fi

    local mount_efifs="${2}"
    if [ -z "${mount_efifs}" ]; then
        echo "image_lib.install_secureboot_certs: missing mount_efifs parameter" >&2
        return 1
    fi

    local efibootdir="${3}"
    if [ -z "${efibootdir}" ]; then
        echo "image_lib.install_secureboot_certs: missing efibootdir parameter" >&2
        return 1
    fi

    local sbcert="${ostree_deploy_rootfs}/etc/portage/secureboot.pem"
    if [ -f "${sbcert}" ]; then
    echo "Copying SecureBoot cert to EFI partition ..."
        cp "${sbcert}" "${mount_efifs}/matrixos-secureboot-cert.pem"

        echo "Generating SecureBoot MOK ..."
        openssl x509 -in "${sbcert}" \
            -outform DER -out "${mount_efifs}/matrixos-secureboot-mok.der"
    else
        echo "NO SECUREBOOT CERT AT: ${sbcert} -- ignoring." >&2
    fi

    local sbkek="${ostree_deploy_rootfs}/etc/portage/secureboot-kek.pem"
    if [ -f "${sbkek}" ]; then
    echo "Copying SecureBoot KEK cert to EFI partition ..."
        cp "${sbkek}" "${mount_efifs}/matrixos-secureboot-kek.pem"

        echo "Generating SecureBoot KEK DER for convenience ..."
        openssl x509 -in "${sbkek}" \
            -outform DER -out "${mount_efifs}/matrixos-secureboot-kek.der"
    else
        echo "NO SECUREBOOT CERT AT: ${sbkek} -- ignoring." >&2
    fi

    local shim_dir="${ostree_deploy_rootfs}/usr/share/shim"
    echo "Copying shim for Secureboot from ${shim_dir} to ${efibootdir} ..."
    cp -v "${shim_dir}"/* "${efibootdir}/"
}

image_lib.install_memtest() {
    local ostree_deploy_rootfs="${1}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "image_lib.install_memtest: missing ostree_deploy_rootfs parameter" >&2
        return 1
    fi

    local efibootdir="${2}"
    if [ -z "${efibootdir}" ]; then
        echo "image_lib.install_memtest: missing efibootdir parameter" >&2
        return 1
    fi

    # Setting up memtest86+.
    local memtest_bin="${ostree_deploy_rootfs}/usr/share/memtest86+/memtest.efi64"
    if [ ! -e "${memtest_bin}" ]; then
        echo "WARNING: ${memtest_bin} not available, please install memtest86+" >&2
        return 0
    fi
    cp "${memtest_bin}" "${efibootdir}/memtest86plus.efi"
}

image_lib.get_kernel_path() {
    local ostree_deploy_rootfs="${1}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "Missing ostree ostree_deploy_rootfs parameter in get_kernel_path" >&2
        return 1
    fi

    local modulesdir="${ostree_deploy_rootfs}/usr/lib/modules"
    if [ -z "${modulesdir}" ]; then
        echo "Unable to determine modules dir" >&2
        return 1
    fi

    # || true for SIGPIPE
    local kernel_ver=
    kernel_ver=$(ls -1 "${modulesdir}"/ | sort -n | head -n 1 || true)
    if [ -z "${kernel_ver}" ]; then
        echo "No kernel dir in ${modulesdir}/" >&2
        return 1
    fi
    echo "${kernel_ver}"
}

image_lib.setup_passwords() {
    local ostree_deploy_rootfs="${1}"
    if [ -z "${ostree_deploy_rootfs}" ]; then
        echo "${0}: missing ostree ostree_deploy_rootfs parameter" >&2
        return 1
    fi

    local shadow_file="${ostree_deploy_rootfs}/etc/shadow"
    local pass_hash=
    local last_change=
    pass_hash=$(openssl passwd -6 matrix)
    last_change=$(($(date +%s) / 86400))
    echo "Setting the default password of matrix to matrix ..."
    sed -i '/^matrix:/d' "${shadow_file}"
    echo "matrix:${pass_hash}:${last_change}:0:99999:7:::" >> "${shadow_file}"
    echo "Setting the default password of root to matrix ..."
    sed -i '/^root:/d' "${shadow_file}"
    echo "root:${pass_hash}:${last_change}:0:99999:7:::" >> "${shadow_file}"
}

image_lib.clear_partition_table() {
    local device_path="${1}"
    if [ -z "${device_path}" ]; then
        echo "image_lib.clear_partition_table: missing device_path parameter" >&2
        return 1
    fi

    echo "Clearing partition table on ${device_path} ..."
    sgdisk -g -o "${device_path}"
    sgdisk -Z "${device_path}"
}

image_lib.get_partition_type() {
    local device_path="${1}"
    if [ -z "${device_path}" ]; then
        echo "image_lib.get_partition_type: missing device_path parameter" >&2
        return 1
    fi
    local pt=
    pt=$(lsblk -no PARTTYPE "${device_path}")
    echo "${pt^^}"  # uppercase to align with sgdisk output.
}

image_lib.dated_fslabel() {
    date +%Y%m%d
}

image_lib.partition_devices() {
    local efi_size="${1}"
    if [ -z "${efi_size}" ]; then
        echo "image_lib.partition_device: missing efi_size parameter" >&2
        return 1
    fi

    local boot_size="${2}"
    if [ -z "${boot_size}" ]; then
        echo "image_lib.partition_device: missing boot_size parameter" >&2
        return 1
    fi

    local image_size="${3}"
    if [ -z "${image_size}" ]; then
        echo "image_lib.partition_device: missing image_size parameter" >&2
        return 1
    fi

    local device_path="${4}"
    if [ -z "${device_path}" ]; then
        echo "image_lib.partition_device: missing device_path parameter" >&2
        return 1
    fi

    echo "Partitioning ${device_path}:"
    echo " --> p1 (EFI: ${efi_size})"
    echo " --> p2 (BOOT: ${boot_size})"
    echo " --> p3 (ROOT: Remainder of ${image_size}, plus autogrow)"
    echo

    sgdisk -n "1:0:+${efi_size}" -t "1:${MATRIXOS_LIVEOS_ESP_PARTTYPE}" "${device_path}"
    sgdisk -n "2:0:+${boot_size}" -t "2:${MATRIXOS_LIVEOS_BOOT_PARTTYPE}" "${device_path}"
    # keep a -10M padding so that systemd-repart does not mark itself as
    # failed.
    sgdisk -n "3:0:-10M" -t "3:${MATRIXOS_LIVEOS_ROOT_PARTTYPE}" "${device_path}"

    # Set the auto-grow flag so that this partition can grow to take
    # the remaining unused space on devices.
    sgdisk -A "3:set:59" "${device_path}"

    echo "Refreshing partition table ..."
    partprobe -s "${device_path}"

    # Just in case.
    udevadm settle
}

image_lib.format_efifs() {
    local efi_device="${1}"
    if [ -z "${efi_device}" ]; then
        echo "image_lib.format_efifs: missing efi_device parameter" >&2
        return 1
    fi

    echo "Creating EFI partition on ${efi_device}"
    local label=
    label=$(image_lib.dated_fslabel)
    mkfs.vfat -F 32 -n "ME${label}" "${efi_device}"
}

image_lib.mount_efifs() {
    local efi_device="${1}"
    if [ -z "${efi_device}" ]; then
        echo "image_lib.mount_efifs: missing efi_device parameter" >&2
        return 1
    fi
    local mount_efifs="${2}"
    if [ -z "${mount_efifs}" ]; then
        echo "image_lib.mount_efifs: missing mount_efifs parameter" >&2
        return 1
    fi

    # because it can be inside rootfs.
    if [ ! -d "${mount_efifs}" ]; then
        echo "Creating ${mount_efifs} ..."
        mkdir -p "${mount_efifs}"
    fi

    echo "Mounting ${efi_device} to ${mount_efifs}"
    mount -t vfat "${efi_device}" "${mount_efifs}"
}

image_lib.format_bootfs() {
    local boot_device="${1}"
    if [ -z "${boot_device}" ]; then
        echo "image_lib.format_bootfs: missing boot_device parameter" >&2
        return 1
    fi

    local label=
    label=$(image_lib.dated_fslabel)

    echo "Creating btrfs on ${boot_device} (boot)"
    mkfs.btrfs -f -L "MB${label}" "${boot_device}"
}

image_lib.mount_bootfs() {
    local boot_device="${1}"
    if [ -z "${boot_device}" ]; then
        echo "image_lib.mount_bootfs: missing boot_device parameter" >&2
        return 1
    fi
    local mount_bootfs="${2}"
    if [ -z "${mount_bootfs}" ]; then
        echo "image_lib.mount_bootfs: missing mount_bootfs parameter" >&2
        return 1
    fi

    # because it can be inside rootfs.
    if [ ! -d "${mount_bootfs}" ]; then
        echo "Creating ${mount_bootfs} ..."
        mkdir -p "${mount_bootfs}"
    fi
    echo "Mounting ${boot_device} to ${mount_bootfs}"
    mount "${boot_device}" "${mount_bootfs}"
}

image_lib.format_rootfs() {
    local root_device="${1}"
    if [ -z "${root_device}" ]; then
        echo "image_lib.format_rootfs: missing root_device parameter" >&2
        return 1
    fi

    local label=
    label=$(image_lib.dated_fslabel)

    echo "Creating btrfs on ${root_device} (root)"
    mkfs.btrfs -f -L "MR${label}" "${root_device}"
}

image_lib.rootfs_kernel_args() {
    local args=(
        "rootflags=discard=async"
    )
    echo "${args[@]}"
}

image_lib.mount_rootfs() {
    local root_device="${1}"
    if [ -z "${root_device}" ]; then
        echo "image_lib.mount_rootfs: missing root_device parameter" >&2
        return 1
    fi
    local mount_rootfs="${2}"
    if [ -z "${mount_rootfs}" ]; then
        echo "image_lib.mount_rootfs: missing mount_rootfs parameter" >&2
        return 1
    fi
    local compression="zstd:6"
    local btrfs_compress="${compression},space_cache=v2,commit=120"
    echo "Mounting ${root_device} to ${mount_rootfs}"
    mount -o compress-force="${btrfs_compress}" "${root_device}" "${mount_rootfs}"
}


image_lib.generate_kernel_boot_args() {
    local -n _boot_args="${1}"
    if [ -z "${1}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing _boot_args parameter" >&2
        return 1
    fi

    local ref="${2}"
    ref=$(ostree_lib.clean_remote_from_ref "${ref}")
    ref=$(ostree_lib.remove_full_from_branch "${ref}")
    if [ -z "${ref}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing ref parameter" >&2
        return 1
    fi

    local efi_device="${3}"
    if [ -z "${efi_device}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing efi_device parameter" >&2
        return 1
    fi
    local boot_device="${4}"
    if [ -z "${boot_device}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing boot_device parameter" >&2
        return 1
    fi
    local physical_root_device="${5}"
    if [ -z "${physical_root_device}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing physical_root_device parameter" >&2
        return 1
    fi
    local root_device="${6}"
    if [ -z "${root_device}" ]; then
        echo "image_lib.generate_kernel_boot_args: missing root_device parameter" >&2
        return 1
    fi
    local encryption_enabled="${7}"  # can be empty.

    local rootfs_args=
    read -ra rootfs_args <<< "$(image_lib.rootfs_kernel_args)"

    local boot_args=(
        "${rootfs_args[@]}"
    )

    local root_device_uuid=
    root_device_uuid=$(fs_lib.device_uuid "${physical_root_device}")
    if [ -z "${root_device_uuid}" ]; then
        echo "Unable to get device UUID for ${root_device}" >&2
        return 1
    fi
    if [ -n "${encryption_enabled}" ]; then
        boot_args+=( "rd.luks.uuid=${root_device_uuid}" )
    fi

    # Tell systemd, since we are not using systemd-boot but grub, to mount
    # ${MATRIXOS_EFI_ROOT} and ${MATRIXOS_BOOT_ROOT}.
    local efi_device_partuuid=
    efi_device_partuuid=$(fs_lib.device_partuuid "${efi_device}")
    if [ -z "${efi_device_partuuid}" ]; then
        echo "Unable to get UUID of EFI partition" >&2
        return 1
    fi
    boot_args+=( "systemd.mount-extra=PARTUUID=${efi_device_partuuid}:${MATRIXOS_EFI_ROOT}:auto:defaults" )

    local boot_device_partuuid=
    boot_device_partuuid=$(fs_lib.device_partuuid "${boot_device}")
    if [ -z "${boot_device_partuuid}" ]; then
        echo "Unable to get UUID of boot partition" >&2
        return 1
    fi
    boot_args+=( "systemd.mount-extra=PARTUUID=${boot_device_partuuid}:${MATRIXOS_BOOT_ROOT}:auto:defaults" )

    local image_cmdline_file="${MATRIXOS_DEV_DIR}/image/boot/${ref}/cmdline.conf"
    if [ -e "${image_cmdline_file}" ]; then
        echo "Reading additional kernel cmdline params from ${image_cmdline_file} ..."
        local lines=
        # Read the file, skip comments and spaces.
        readarray -t lines < <(grep -vE '^[[:space:]]*($|#)' "${image_cmdline_file}")
        for line in "${lines[@]}"; do
            boot_args+=( "${line}" )
        done
    else
        echo "WARNING: no additional kernel cmdline params available, ${image_cmdline_file} does not exist." >&2
    fi

    _boot_args=( "${boot_args[@]}" )
}

image_lib.package_list() {
    local -n _pkg_list="${1}"
    if [ -z "${1}" ]; then
        echo "image_lib.package_list: missing _pkg_list parameter" >&2
        return 1
    fi
    local rootfs="${2}"
    if [ -z "${rootfs}" ]; then
        echo "image_lib.package_list: missing rootfs parameter" >&2
        return 1
    fi

    local vdb="${rootfs%/}${MATRIXOS_RO_VDB}"
    if [ ! -d "${vdb}" ]; then
        echo "image_lib.package_list: ${vdb} does not exist. cannot generate pkglist" >&2
        return 0
    fi

    local d=
    for d in "${vdb}"/*/*; do
        d="${d#${vdb}/}"
        _pkg_list+=( "${d}" )
    done
    echo "Generated package list:"
    for pkg in "${_pkg_list[@]}"; do
        echo ">> ${pkg}"
    done
}

image_lib.finalize_filesystems() {
    local mount_rootfs="${1}"
    if [ -z "${mount_rootfs}" ]; then
        echo "image_lib.finalize_filesystems: missing mount_rootfs parameter" >&2
        return 1
    fi
    local mount_bootfs="${2}"
    if [ -z "${mount_bootfs}" ]; then
        echo "image_lib.finalize_filesystems: missing mount_bootfs parameter" >&2
        return 1
    fi
    local mount_efifs="${3}"
    if [ -z "${mount_efifs}" ]; then
        echo "image_lib.finalize_filesystems: missing mount_efifs parameter" >&2
        return 1
    fi

    # Executing fstrim to help with sparse image file compression ratio.
    echo "Executing fstrim on ${mount_rootfs}"
    # Some usb sticks do not support fstrim.
    fstrim -v "${mount_rootfs}" || true

    echo "Executing fstrim on ${mount_bootfs}"
    # Some usb sticks do not support fstrim.
    fstrim -v "${mount_bootfs}" || true
}

image_lib.qcow2_image_path() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "image_lib.qcow2_image_path: missing image_path parameter" >&2
        return 1
    fi
    echo "${image_path}.qcow2"
}

image_lib.create_qcow2_image() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "image_lib.create_qcow2_image: missing image_path parameter" >&2
        return 1
    fi

    qemu-img convert -c -O qcow2 -p "${image_path}" "$(image_lib.qcow2_image_path "${image_path}")"
}

image_lib.show_final_filesystem_info() {
    local block_device="${1}"
    if [ -z "${block_device}" ]; then
        echo "image_lib.finalize_filesystems: missing block_device parameter" >&2
        return 1
    fi
    local mount_bootfs="${2}"
    if [ -z "${mount_bootfs}" ]; then
        echo "image_lib.finalize_filesystems: missing mount_bootfs parameter" >&2
        return 1
    fi
    local mount_efifs="${3}"
    if [ -z "${mount_efifs}" ]; then
        echo "image_lib.finalize_filesystems: missing mount_efifs parameter" >&2
        return 1
    fi

    echo "Final boot partition directory tree:"
    find "${mount_bootfs}"
    echo "Final EFI partition directory tree:"
    find "${mount_efifs}"
    echo "Block devices on ${block_device}:"
    blkid ${block_device}*
    echo "Filesystem setup complete!"
}

image_lib.show_test_info() {
    if [[ "${#@}" -eq 0 ]]; then
        echo "show_test_info: missing artifacts array parameter" >&2
        return 1
    fi

    echo "Generated artifacts:"
    for artifact in "${@}"; do
        echo ">> ${artifact}"
    done

    echo
    echo "How to test (as user):"
    echo "cp /usr/share/edk2-ovmf/OVMF_VARS.fd ./my_vars.fd"
    echo "qemu-system-x86_64 -enable-kvm -m 8G \\
    -drive if=pflash,format=raw,readonly=on,file=/usr/share/edk2-ovmf/OVMF_CODE.fd \\
    -drive if=pflash,format=raw,file=./my_vars.fd \\
    -drive file=IMAGE_PATH,format=raw \\
    -device virtio-vga-gl,hostmem=512M,blob=true,venus=on -display gtk,gl=on -cpu host -smp 4 \\
    -nic user,model=virtio-net-pci,hostfwd=tcp::2222-:22 \\
    -audiodev pa,id=snd0 -device intel-hda -device hda-duplex,audiodev=snd0"
    echo
    echo "To move to a USB stick:"
    echo "    dd if=IMAGE_PATH of=/dev/sdX bs=4M conv=sparse,sync status=progress"
    echo
}

image_lib.remove_image_file() {
    local image_path="${1}"
    if [ -z "${image_path}" ]; then
        echo "remove_image_file: missing image_path parameter" >&2
        return 1
    fi
    echo "Removing ${image_path} ..."
    rm -f "${image_path}"
    rm -f "${image_path}.sha256"
    rm -f "${image_path}.asc"
}

image_lib.image_lock_dir() {
    local lock_dir="${MATRIXOS_IMAGE_LOCK_DIR}"
    mkdir -p "${lock_dir}"
    echo "${lock_dir}"
}

image_lib.image_lock_path() {
    local ref="${1}"
    if [ -z "${ref}" ]; then
        echo "image_lib.image_lock_path: missing ref parameter" >&2
        return 1
    fi

    local lock_dir=
    local lock_file=
    lock_dir="$(image_lib.image_lock_dir)"
    lock_file="${lock_dir}/${ref}.lock"

    mkdir -p "$(dirname "${lock_file}")"
    echo "${lock_file}"
}

image_lib.execute_with_image_lock() {
    local func="${1}"
    local ref="${2}"
    shift 2

    local lock_path=
    lock_path=$(image_lib.image_lock_path "${ref}")
    echo "Acquiring branch ${ref} lock via ${lock_path} ..."

    local lock_fd=
    # Do not use a subshell otherwise the global cleanup variables used in trap will not
    # be filled properly. Like: ${MOUNTS} in seeder.
    exec {lock_fd}>"${lock_path}"

    if ! flock -x --timeout "${MATRIXOS_IMAGE_LOCK_WAIT_SECS}" "${lock_fd}"; then
        echo "Timed out waiting for imager lock ${lock_path}" >&2
        exec {lock_fd}>&-
        return 1
    fi

    echo "Lock for imager ${ref}, ${lock_path} acquired!"

    # We do NOT use a trap. We rely on standard flow control.
    # If "${func}" crashes (set -e), the script dies and OS closes the FD.
    # If "${func}" returns (success or fail), we capture it.
    "${func}" "${@}"
    local ret=${?}

    # Release the lock.
    exec {lock_fd}>&-
    return ${ret}
}