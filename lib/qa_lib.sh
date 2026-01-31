#!/bin/bash
set -eu

source "${MATRIXOS_DEV_DIR:-/matrixos}"/headers/env.include.sh


qa_lib.root_privs() {
    local uid=
    uid=$(id -u)
    if [ "${uid}" != "0" ]; then
        echo "Run ${0} as root." >&2
        return 1
    fi
}

qa_lib.check_matrixos_private() {
    local matrixos_private="${MATRIXOS_PRIVATE_GIT_REPO_PATH}"
    if [ ! -d "${matrixos_private}" ]; then
        echo "${matrixos_private} does not exist ..." >&2
        echo "Please set a valid MATRIXOS_PRIVATE_GIT_REPO_PATH directory path." >&2
        echo "See README.md and https://github.com/lxnay/matrixos-private-example for more details." >&2
        echo "This directory contains YOUR GPG private keys and SecureBoot certs necessary to build" >&2
        echo "and release a custom matrixOS Gentoo build." >&2
        return 1
    fi
}

qa_lib.check_secureboot() {
    local imagedir="${1}"
    local sbcert_path="${2}"

    local modulesdir="${imagedir}/lib/modules"
    local usb_storage_mods=
    read -ra usb_storage_mods <<< "$(find "${modulesdir}" -type f -name "usb-storage.ko*")"
    if [[ "${#usb_storage_mods[@]}" -eq 0 ]]; then
        echo "No usb-storage.ko found in ${modulesdir}" >&2
        return 1
    fi

    local sb_serial=
    sb_serial=$(openssl x509 -in "${sbcert_path}" -noout -serial \
        | cut -d'=' -f2 | sed 's/..\B/&:/g')
    if [ -z "${sb_serial}" ]; then
        echo "Cannot extract SecureBoot serial from: ${sbcert_path}"
        return 1
    fi
    echo "SecureBoot Serial of cert ${sbcert_path}: '${sb_serial}' ::"

    # Check SecureBoot signing key:
    local mod=
    for mod in "${usb_storage_mods[@]}"; do
        mod="${mod#${imagedir%/}}"
        echo "Checking module signature for: ${mod}"
        local sig_key=
        sig_key=$(chroot "${imagedir}" modinfo -F sig_key "${mod}")
        if [ -z "${sig_key}" ]; then
            echo "No sig_key found for ${mod}" >&2
            return 1
        fi
        if [ "${sb_serial}" != "${sig_key}" ]; then
            echo "ERROR: ${mod} SecureBoot serial and module signature key mismatch." >&2
            echo "SecureBoot serial: '${sb_serial}'" >&2
            echo "Module key signature: '${sig_key}'" >&2
            return 1
        fi
    done
}

qa_lib._verify_environment_setup() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib._verify_environment_setup: missing parameter imagedir" >&2
        return 1
    fi
    shift

    local executables=()
    local dirs=()
    local split=
    for exe in "${@}"; do
        if [ "${exe}" = "--" ]; then
            split=1
            continue
        fi
        if [ -n "${split}" ]; then
            dirs+=( "${exe}" )
        else
            executables+=( "${exe}" )
        fi
    done

    local ret=0

    for exe in "${executables[@]}"; do
        if [[ "${exe}" != */* ]]; then
            if [ "${imagedir}" = "/" ]; then
                if ! command -v "${exe}" > /dev/null; then
                    echo "${exe} not found" >&2
                    ret=1
                fi
            else
                local found=
                found=$(chroot "${imagedir}" which "${exe}" 2>/dev/null || true)
                if [ -z "${found}" ]; then
                    echo "${exe} not found" >&2
                    ret=1
                fi
            fi
        else
            if [ "${imagedir}" = "/" ]; then
                if [ ! -x "${exe}" ]; then
                    echo "${exe} not found" >&2
                    ret=1
                fi
            else
                if [ ! -x "${imagedir}${exe}" ]; then
                    echo "${imagedir}${exe} not found" >&2
                    ret=1
                fi
            fi
        fi
    done

    for dir in "${dirs[@]}"; do
        if [ ! -d "${imagedir%/}${dir}" ]; then
            echo "${imagedir%/}${dir} not found" >&2
            ret=1
        fi
    done

    return ${ret}
}

# Every distro should ship with these binaries or directories.
qa_lib.verify_distro_rootfs_environment_setup() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.verify_distro_rootfs_environment_setup: missing parameter imagedir" >&2
        return 1
    fi

    local executables=(
        btrfs
        chroot
        cryptsetup
        find
        findmnt
        fstrim
        gpg
        losetup
        mkfs.btrfs
        mkfs.vfat
        openssl
        ostree
        partprobe
        qemu-img
        sha256sum
        sgdisk
        udevadm
        wget
        xz
    )
    if [ "${imagedir}" != "/" ]; then
        # inside chroot, we reference this exactly in the script.
        executables+=( /usr/bin/grub-install )
    fi
    local dirs=(
        /usr/share/shim
    )
    qa_lib._verify_environment_setup "${imagedir}" "${executables[@]}" "--" "${dirs[@]}"
}

qa_lib.verify_releaser_environment_setup() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.verify_releaser_environment_setup: missing parameter imagedir" >&2
        return 1
    fi

    local executables=(
        chroot
        find
        findmnt
        gpg
        openssl
        ostree
    )
    local dirs=(
        "${MATRIXOS_PRIVATE_GIT_REPO_PATH}"
    )
    qa_lib._verify_environment_setup "${imagedir}" "${executables[@]}" "--" "${dirs[@]}"
}

qa_lib.verify_seeder_environment_setup() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.verify_seeder_environment_setup: missing parameter imagedir" >&2
        return 1
    fi
    local executables=(
        chroot
        gpg
        openssl
        ostree
        wget
    )
    local dirs=(
        "${MATRIXOS_PRIVATE_GIT_REPO_PATH}"
    )
    qa_lib._verify_environment_setup "${imagedir}" "${executables[@]}" "--" "${dirs[@]}"
}

qa_lib.verify_imager_environment_setup() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.verify_imager_environment_setup: missing parameter imagedir" >&2
        return 1
    fi
    local gpg_enabled="${2}"  # can be empty.

    local executables=(
        btrfs
        cryptsetup
        findmnt
        fstrim
        gpg
        grub-install
        losetup
        mkfs.vfat
        mkfs.btrfs
        openssl
        ostree
        partprobe
        qemu-img
        sha256sum
        sgdisk
        udevadm
        xz
    )
    local dirs=(
        /usr/share/shim
    )
    qa_lib._verify_environment_setup "${imagedir}" "${executables[@]}" "--" "${dirs[@]}"
}

qa_lib.check_kernel_and_external_module() {
    local imagedir="${1}"
    local module_name="${2}"
    if [ -z "${module_name}" ] || [ -z "${imagedir}" ]; then
        echo "qa_lib.check_kernel_and_external_module: missing parameters imagedir and module name" >&2
        return 1
    fi

    local modulesdir="${imagedir}/lib/modules"

    local vmlinuzes=
    vmlinuzes=$(ls -1 "${imagedir}/lib/modules"/*/vmlinuz)
    local vmlinuz_count=0
    for vmlinuz in ${vmlinuzes}; do
        echo "Found kernel: ${vmlinuz}"
        vmlinuz_count=$((vmlinuz_count+1))
    done

    local initramfses=
    initramfses=$(ls -1 "${imagedir}/lib/modules"/*/initramfs)
    local initramfs_count=0
    for initramfs in ${initramfses}; do
        echo "Found initramfs: ${initramfs}"
        initramfs_count=$((initramfs_count+1))
    done

    if [ "${vmlinuz_count}" = "0" ]; then
        echo "No kernel found. Refusing to release." >&2
        return 1
    fi
    if [ "${initramfs_count}" = "0" ]; then
        echo "No initramfs found. Refusing to release." >&2
        return 1
    fi
    if [ "${vmlinuz_count}" != "${initramfs_count}" ]; then
        echo "vmlinuz found: ${vmlinuz_count} -- initramfs found: ${initramfs_count}. Refusing to release." >&2
        return 1
    fi

    local kernel_mods=
    read -ra kernel_mods <<< "$(find "${modulesdir}"/* -type f -name "${module_name}")"
    if [[ "${#kernel_mods[@]}" -eq 0 ]]; then
        echo "No ${module_name} found in ${modulesdir}" >&2
        return 1
    fi

    local kernel_mod=
    local kernel_mod_vermagic=
    local module_kernel_ver=
    local corresponding_vmlinuz=
    local vmlinuz_kernel_ver=
    local mod_count=0
    local failure=
    for kernel_mod in "${kernel_mods[@]}"; do
        kernel_mod="${kernel_mod#${imagedir%/}}"
        mod_count=$((mod_count+1))
        echo "Testing module: ${kernel_mod}"

        kernel_mod_vermagic=$(chroot "${imagedir}" modinfo -F vermagic "${kernel_mod}")
        module_kernel_ver=$(echo "${kernel_mod_vermagic}" | awk '{print $1}')
        echo "${kernel_mod}: vermagic is: ${kernel_mod_vermagic}, kernel ver is: ${module_kernel_ver}"

        corresponding_vmlinuz="${modulesdir}/${module_kernel_ver}/vmlinuz"
        if [ ! -e "${corresponding_vmlinuz}" ]; then
            echo "${corresponding_vmlinuz} not found for related ${kernel_mod}. Refusing to release." >&2
            failure=1
            continue
        fi

        vmlinuz_kernel_ver=$(file -b "${corresponding_vmlinuz}" | grep -oP 'version \K[^ ]+')
        if [ "${vmlinuz_kernel_ver}" != "${module_kernel_ver}" ]; then
            echo "${kernel_mod}: mismatch in kernel ver: (M) ${module_kernel_ver} vs (K) ${vmlinuz_kernel_ver}" >&2
            failure=1
            continue
        fi
    done
    if [ -n "${failure}" ]; then
        return 1
    fi

    if [ "${mod_count}" != "${vmlinuz_count}" ]; then
        echo "Unexpected number of ${module_name} files found! Refusing to release." >&2
        echo "Number of ${module_name} modules: ${mod_count} -- vmlinuz found: ${vmlinuz_count}" >&2
        echo "${kernel_mods[@]}" >&2
        return 1
    fi
}

qa_lib.check_nvidia_module() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.check_nvidia_module: missing parameter imagedir" >&2
        return 1
    fi
    if [ ! -d "${imagedir}" ]; then
        echo "qa_lib.check_nvidia_module: ${imagedir} is not a directory" >&2
        return 1
    fi

    if [ ! -d "${imagedir}"/var/db/pkg/x11-drivers/nvidia-drivers* ]; then
        echo "x11-drivers/nvidia-drivers* not installed, skipping QA check"
        return 0
    fi
    qa_lib.check_kernel_and_external_module "${imagedir}" "nvidia.ko*"
}

qa_lib.check_ryzen_smu_module() {
    local imagedir="${1}"
    if [ -z "${imagedir}" ]; then
        echo "qa_lib.check_ryzen_smu_module: missing parameter imagedir" >&2
        return 1
    fi
    if [ ! -d "${imagedir}" ]; then
        echo "qa_lib.check_ryzen_smu_module: ${imagedir} is not a directory" >&2
        return 1
    fi

    if [ ! -d "${imagedir}"/var/db/pkg/app-admin/ryzen_smu* ]; then
        echo "app-admin/ryzen_smu* not installed, skipping QA check"
        return 0
    fi

    qa_lib.check_kernel_and_external_module "${imagedir}" "ryzen_smu.ko*"
}

qa_lib.check_number_of_kernels() {
    local imagedir="${1}"
    local expected_amount="${2}"

    local found_amount=
    found_amount=$(find "${imagedir}"/usr/lib/modules/* -name vmlinuz | wc -l)
    if [ "${found_amount}" != "${expected_amount}" ]; then
        echo "Found ${found_amount} kernels in /usr/lib/modules, expected ${expected_amount}" >&2
        ls -1 "${imagedir}"/usr/lib/modules/ -la >&2
        return 1
    fi
    return 0
}
