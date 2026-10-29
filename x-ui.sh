#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
yellow='\033[0;33m'
plain='\033[0m'

#Add some basic function here
function LOGD() {
    echo -e "${yellow}[DEG] $* ${plain}"
}

function LOGE() {
    echo -e "${red}[ERR] $* ${plain}"
}

function LOGI() {
    echo -e "${green}[INF] $* ${plain}"
}
# check root
[[ $EUID -ne 0 ]] && LOGE "错误：请使用 sudo 或 root 运行此脚本！ \n" && exit 1

# Check OS and set release variable
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "Failed to check the system OS, please contact the author!" >&2
    exit 1
fi

echo "系统发行版： $release"

confirm() {
    if [[ $# > 1 ]]; then
        echo && read -p "$1 [默认 $2]: " temp
        if [[ x"${temp}" == x"" ]]; then
            temp=$2
        fi
    else
        read -p "$1 [y/n]: " temp
    fi
    if [[ x"${temp}" == x"y" || x"${temp}" == x"Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "是否重启面板？重启面板也会重启 Xray" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}按回车键返回主菜单： ${plain}" && read temp
    show_menu
}

local_package_action() {
    local action=$1 package_dir=${2:-} port=${3:-}
    if [[ -z "$package_dir" ]]; then
        if [[ ! -t 0 ]]; then
            LOGE "需要指定本地安装包目录。"
            return 2
        fi
        read -r -p "本地安装包目录：" package_dir
    fi
    if [[ -z "$package_dir" || ! -x "$package_dir/xui-manage-local" ]]; then
        LOGE "本地安装包中未找到 xui-manage-local。"
        return 1
    fi
    if [[ "$action" == install ]]; then
        if [[ -z "$port" && -t 0 ]]; then
            read -r -p "面板端口 [2053]：" port
        fi
        "$package_dir/xui-manage-local" install "$package_dir" "${port:-2053}"
    else
        "$package_dir/xui-manage-local" "$action" "$package_dir"
    fi
}

install() { local_package_action install "${1:-}" "${2:-}"; }
update() { local_package_action upgrade "${1:-}"; }
recover() { local_package_action recover "${1:-}"; }

# Function to handle the deletion of the script file
delete_script() {
    rm "$0" # Remove the script file itself
    exit 1
}

uninstall() {
    confirm "确定卸载面板吗？Xray 也将被卸载！" " n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    systemctl stop x-ui
    systemctl disable x-ui
    rm /etc/systemd/system/x-ui.service -f
    systemctl daemon-reload
    systemctl reset-failed
    rm /etc/x-ui/ -rf
    rm /usr/local/x-ui/ -rf
    echo -e "\nUninstalled Successfully."
    echo ""
    echo -e "If you need to install this panel again, you can use below command:"
    echo -e "Build from the local source checkout; see README.md."
    echo ""
    # Trap the SIGTERM signal
    trap delete_script SIGTERM
    delete_script
}

reset_user() {
    confirm "确定重置面板用户名和密码吗？" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi

    read -rp "请输入登录用户名 [留空随机生成]：" config_account
    [[ -z $config_account ]] && config_account=$(gen_random_string 10)
    read -rp "请输入登录密码 [留空随机生成]：" config_password
    [[ -z $config_password ]] && config_password=$(gen_random_string 18)
    /usr/local/x-ui/x-ui setting -username "${config_account}" -password "${config_password}"
    
    echo -e "面板登录用户名已重置为： ${green} ${config_account} ${plain}"
    echo -e "面板登录密码已重置为： ${green} ${config_password} ${plain}"
    echo -e "${green}请使用新用户名和密码登录面板，并妥善保存。 ${plain}"

    confirm_restart
}

gen_random_string() {
    local length="$1"
    local random_string=$(LC_ALL=C tr -dc 'a-zA-Z0-9' </dev/urandom | fold -w "$length" | head -n 1)
    echo "$random_string"
}

reset_webbasepath() {
    echo -e "${yellow}重置面板访问路径${plain}"

    read -rp "确定重置面板访问路径吗？(y/n)：" confirm
    if [[ $confirm != "y" && $confirm != "Y" ]]; then
        echo -e "${yellow}操作已取消。${plain}"
        return
    fi

    config_webBasePath=$(gen_random_string 10)

    # Apply the new web base path setting
    /usr/local/x-ui/x-ui setting -webBasePath "${config_webBasePath}" >/dev/null 2>&1

    echo -e "面板访问路径已重置为： ${green}${config_webBasePath}${plain}"
    echo -e "${green}请使用新的访问路径打开面板。${plain}"
    restart
}

reset_config() {
    confirm "确定重置所有面板设置吗？账户数据和用户名密码不会改变" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    /usr/local/x-ui/x-ui setting -reset
    echo -e "面板设置已重置。请重启面板并使用默认端口 ${green}54321${plain} 访问面板"
    confirm_restart
}

check_config() {
    info=$(/usr/local/x-ui/x-ui setting -show true)
    if [[ $? != 0 ]]; then
        LOGE "获取当前设置失败，请查看日志"
        show_menu
    fi
    LOGI "${info}"
}

get_uri() {
    info=$(/usr/local/x-ui/x-ui uri)
    if [[ $? != 0 ]]; then
        LOGE "获取当前访问地址失败"
        show_menu
    fi
    LOGI "可以通过以下地址访问面板："
    echo -e "${yellow}${info}${plain}"
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

set_port() {
    echo && echo -n -e "请输入端口号 [1-65535]：" && read port
    if [[ -z "${port}" ]]; then
        LOGD "已取消"
        before_show_menu
    else
        /usr/local/x-ui/x-ui setting -port ${port}
        echo -e "端口已设置。请重启面板并使用新端口 ${green}${port}${plain} 访问面板"
        confirm_restart
    fi
}

start() {
    check_status
    if [[ $? == 0 ]]; then
        echo ""
        LOGI "面板正在运行；如需重新启动，请选择重启"
    else
        systemctl start x-ui
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            LOGI "面板启动成功"
        else
            LOGE "面板未在两秒内启动，请稍后检查日志"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    check_status
    if [[ $? == 1 ]]; then
        echo ""
        LOGI "面板已停止，无需重复操作"
    else
        systemctl stop x-ui
        sleep 2
        check_status
        if [[ $? == 1 ]]; then
            LOGI "面板和 Xray 已停止"
        else
            LOGE "面板未在两秒内停止，请稍后检查日志"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart() {
    systemctl restart x-ui
    sleep 2
    check_status
    if [[ $? == 0 ]]; then
        LOGI "面板和 Xray 重启成功"
    else
        LOGE "面板未在两秒内重启，请稍后检查日志"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart_xray() {
    systemctl reload x-ui
    LOGI "已发送 Xray 重启信号，请查看日志确认结果"
    sleep 2
    show_xray_status
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    systemctl status x-ui -l
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    systemctl enable x-ui
    if [[ $? == 0 ]]; then
        LOGI "已启用面板开机自启"
    else
        LOGE "启用开机自启失败"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    systemctl disable x-ui
    if [[ $? == 0 ]]; then
        LOGI "已关闭面板开机自启"
    else
        LOGE "关闭开机自启失败"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    echo -e "${green}\t1.${plain} 查看实时日志"
    echo -e "${green}\t2.${plain} 清理全部系统日志"
    echo -e "${green}\t0.${plain} 返回主菜单"
    read -p "请选择：" choice

    case "$choice" in
    0)
        return
        ;;
    1)
        journalctl -u x-ui -e --no-pager -f -p debug
        if [[ $# == 0 ]]; then
        before_show_menu
        fi
        ;;
    2)
        sudo journalctl --rotate
        sudo journalctl --vacuum-time=1s
        echo "日志已清理。"
        restart
        ;;
    *)
        echo "无效的选项"
        ;;
    esac
}

update_shell() {
    if [[ ! -f /usr/local/x-ui/x-ui.sh ]]; then
        LOGE "在 /usr/local/x-ui 中未找到管理脚本。"
        return 1
    fi
    if cp -- /usr/local/x-ui/x-ui.sh /usr/bin/x-ui && chmod +x /usr/bin/x-ui; then
        LOGI "Updated the management script from the local installation."
    else
        LOGE "Failed to update the local management script."
        return 1
    fi
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ ! -f /etc/systemd/system/x-ui.service ]]; then
        return 2
    fi
    temp=$(systemctl status x-ui | grep Active | awk '{print $3}' | cut -d "(" -f2 | cut -d ")" -f1)
    if [[ x"${temp}" == x"running" ]]; then
        return 0
    else
        return 1
    fi
}

check_enabled() {
    temp=$(systemctl is-enabled x-ui)
    if [[ x"${temp}" == x"enabled" ]]; then
        return 0
    else
        return 1
    fi
}

check_uninstall() {
    check_status
    if [[ $? != 2 ]]; then
        echo ""
        LOGE "面板已安装，请勿重复安装"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

check_install() {
    check_status
    if [[ $? == 2 ]]; then
        echo ""
        LOGE "请先安装面板"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

show_status() {
    check_status
    case $? in
    0)
        echo -e "面板状态： ${green}运行中${plain}"
        show_enable_status
        ;;
    1)
        echo -e "面板状态： ${yellow}未运行${plain}"
        show_enable_status
        ;;
    2)
        echo -e "面板状态： ${red}未安装${plain}"
        ;;
    esac
    show_xray_status
}

show_enable_status() {
    check_enabled
    if [[ $? == 0 ]]; then
        echo -e "开机自启： ${green}是${plain}"
    else
        echo -e "开机自启： ${red}否${plain}"
    fi
}

check_xray_status() {
    count=$(ps -ef | grep "xray-linux" | grep -v "grep" | wc -l)
    if [[ count -ne 0 ]]; then
        return 0
    else
        return 1
    fi
}

show_xray_status() {
    check_xray_status
    if [[ $? == 0 ]]; then
        echo -e "Xray 状态： ${green}运行中${plain}"
    else
        echo -e "Xray 状态： ${red}未运行${plain}"
    fi
}

install_acme() {
    # Check if acme.sh is already installed
    if command -v ~/.acme.sh/acme.sh &>/dev/null; then
        LOGI "acme.sh 已安装。"
        return 0
    fi

    LOGI "正在安装 acme.sh…"
    cd ~ || return 1 # Ensure you can change to the home directory

    curl -s https://get.acme.sh | sh
    if [ $? -ne 0 ]; then
        LOGE "acme.sh 安装失败。"
        return 1
    else
        LOGI "acme.sh 安装成功。"
    fi

    return 0
}

ssl_cert_issue_main() {
    echo -e "${green}\t1.${plain} 申请 SSL 证书"
    echo -e "${green}\t2.${plain} 撤销证书"
    echo -e "${green}\t3.${plain} 强制续期"
    echo -e "${green}\t4.${plain} 查看已有域名"
    echo -e "${green}\t5.${plain} 设置面板证书路径"
    echo -e "${green}\t0.${plain} 返回主菜单"

    read -p "请选择：" choice
    case "$choice" in
    0)
        show_menu
        ;;
    1)
        ssl_cert_issue
        ;;
    2)
        local domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
        if [ -z "$domains" ]; then
            echo "没有可撤销的证书。"
        else
            echo "Existing domains:"
            echo "$domains"
            read -p "Please enter a domain from the list to revoke the certificate: " domain
            if echo "$domains" | grep -qw "$domain"; then
                ~/.acme.sh/acme.sh --revoke -d ${domain}
                LOGI "Certificate revoked for domain: $domain"
            else
                echo "Invalid domain entered."
            fi
        fi
        ;;
    3)
        local domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
        if [ -z "$domains" ]; then
            echo "没有可续期的证书。"
        else
            echo "Existing domains:"
            echo "$domains"
            read -p "Please enter a domain from the list to renew the SSL certificate: " domain
            if echo "$domains" | grep -qw "$domain"; then
                ~/.acme.sh/acme.sh --renew -d ${domain} --force
                LOGI "Certificate forcefully renewed for domain: $domain"
            else
                echo "Invalid domain entered."
            fi
        fi
        ;;
    4)
        local domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
        if [ -z "$domains" ]; then
            echo "未找到证书。"
        else
            echo "Existing domains and their paths:"
            for domain in $domains; do
                local cert_path="/root/cert/${domain}/fullchain.pem"
                local key_path="/root/cert/${domain}/privkey.pem"
                if [[ -f "${cert_path}" && -f "${key_path}" ]]; then
                    echo -e "Domain: ${domain}"
                    echo -e "\tCertificate Path: ${cert_path}"
                    echo -e "\tPrivate Key Path: ${key_path}"
                else
                    echo -e "Domain: ${domain} - Certificate or Key missing."
                fi
            done
        fi
        ;;
    5)
        local domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)
        if [ -z "$domains" ]; then
            echo "未找到证书。"
        else
            echo "Available domains:"
            echo "$domains"
            read -p "Please choose a domain to set the panel paths: " domain

            if echo "$domains" | grep -qw "$domain"; then
                local webCertFile="/root/cert/${domain}/fullchain.pem"
                local webKeyFile="/root/cert/${domain}/privkey.pem"

                if [[ -f "${webCertFile}" && -f "${webKeyFile}" ]]; then
                    /usr/local/x-ui/x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
                    echo "Panel paths set for domain: $domain"
                    echo "  - Certificate File: $webCertFile"
                    echo "  - Private Key File: $webKeyFile"
                    restart
                else
                    echo "Certificate or private key not found for domain: $domain."
                fi
            else
                echo "Invalid domain entered."
            fi
        fi
        ;;

    *)
        echo "无效的选项"
        ;;
    esac
}

ssl_cert_issue() {
    # check for acme.sh first
    if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
        echo "acme.sh could not be found. we will install it"
        install_acme
        if [ $? -ne 0 ]; then
            LOGE "install acme failed, please check logs"
            exit 1
        fi
    fi

    # install socat second
    case "${release}" in
    ubuntu | debian | armbian)
        apt update && apt install socat -y
        ;;
    centos | almalinux | rocky | ol)
        yum -y update && yum -y install socat
        ;;
    fedora | amzn)
        dnf -y update && dnf -y install socat
        ;;
    arch | manjaro | parch)
        pacman -Sy --noconfirm socat
        ;;
    *)
        echo -e "${red}Unsupported operating system. Please check the script and install the necessary packages manually.${plain}\n"
        exit 1
        ;;
    esac
    if [ $? -ne 0 ]; then
        LOGE "install socat failed, please check logs"
        exit 1
    else
        LOGI "install socat succeed..."
    fi

    # get the domain here, and we need to verify it
    local domain=""
    read -p "Please enter your domain name: " domain
    LOGD "Your domain is: ${domain}, checking it..."

    # check if there already exists a certificate
    local currentCert=$(~/.acme.sh/acme.sh --list | tail -1 | awk '{print $1}')
    if [ "${currentCert}" == "${domain}" ]; then
        local certInfo=$(~/.acme.sh/acme.sh --list)
        LOGE "System already has certificates for this domain. Cannot issue again. Current certificate details:"
        LOGI "$certInfo"
        exit 1
    else
        LOGI "Your domain is ready for issuing certificates now..."
    fi

    # create a directory for the certificate
    certPath="/root/cert/${domain}"
    if [ ! -d "$certPath" ]; then
        mkdir -p "$certPath"
    else
        rm -rf "$certPath"
        mkdir -p "$certPath"
    fi

    # get the port number for the standalone server
    local WebPort=80
    read -p "Please choose which port to use (default is 80): " WebPort
    if [[ ${WebPort} -gt 65535 || ${WebPort} -lt 1 ]]; then
        LOGE "Your input ${WebPort} is invalid, will use default port 80."
        WebPort=80
    fi
    LOGI "Will use port: ${WebPort} to issue certificates. Please make sure this port is open."

    # issue the certificate
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt
    ~/.acme.sh/acme.sh --issue -d ${domain} --listen-v6 --standalone --httpport ${WebPort}
    if [ $? -ne 0 ]; then
        LOGE "Issuing certificate failed, please check logs."
        rm -rf ~/.acme.sh/${domain}
        exit 1
    else
        LOGE "Issuing certificate succeeded, installing certificates..."
    fi

    # install the certificate
    ~/.acme.sh/acme.sh --installcert -d ${domain} \
        --key-file /root/cert/${domain}/privkey.pem \
        --fullchain-file /root/cert/${domain}/fullchain.pem

    if [ $? -ne 0 ]; then
        LOGE "Installing certificate failed, exiting."
        rm -rf ~/.acme.sh/${domain}
        exit 1
    else
        LOGI "Installing certificate succeeded, enabling auto renew..."
    fi

    # enable auto-renew
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade
    if [ $? -ne 0 ]; then
        LOGE "Auto renew failed, certificate details:"
        ls -lah cert/*
        chmod 755 $certPath/*
        exit 1
    else
        LOGI "Auto renew succeeded, certificate details:"
        ls -lah cert/*
        chmod 755 $certPath/*
    fi

    # Prompt user to set panel paths after successful certificate installation
    read -p "Would you like to set this certificate for the panel? (y/n): " setPanel
    if [[ "$setPanel" == "y" || "$setPanel" == "Y" ]]; then
        local webCertFile="/root/cert/${domain}/fullchain.pem"
        local webKeyFile="/root/cert/${domain}/privkey.pem"

        if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
            /usr/local/x-ui/x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
            LOGI "Panel paths set for domain: $domain"
            LOGI "  - Certificate File: $webCertFile"
            LOGI "  - Private Key File: $webKeyFile"
            restart
        else
            LOGE "Error: Certificate or private key file not found for domain: $domain."
        fi
    else
        LOGI "Skipping panel path setting."
    fi
}

ssl_cert_issue_CF() {
    echo -E ""
    LOGD "******使用说明******"
    LOGI "申请证书需要以下信息："
    LOGI "1. Cloudflare 注册邮箱"
    LOGI "2. Cloudflare 全局 API 密钥"
    LOGI "3. 在 Cloudflare 中配置的域名"
    LOGI "4. 证书默认安装路径为 /root/cert"
    confirm "确认继续吗？" "y"
    if [ $? -eq 0 ]; then
        # check for acme.sh first
        if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
            echo "未找到 acme.sh，即将安装"
            install_acme
            if [ $? -ne 0 ]; then
                LOGE "acme.sh 安装失败，请查看日志"
                exit 1
            fi
        fi
        CF_Domain=""
        CF_GlobalKey=""
        CF_AccountEmail=""
        certPath=/root/cert
        if ! mkdir -p -- "$certPath"; then
            LOGE "无法创建证书目录：$certPath"
            return 1
        fi
        LOGD "请设置域名："
        read -p "请输入域名：" CF_Domain
        LOGD "域名已设置为：${CF_Domain}"
        LOGD "请设置 API 密钥："
        read -rs -p "请输入 API 密钥：" CF_GlobalKey
        echo
        LOGD "API 密钥已输入。"
        LOGD "请设置注册邮箱："
        read -p "请输入邮箱：" CF_AccountEmail
        LOGD "注册邮箱为：${CF_AccountEmail}"
        ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt
        if [ $? -ne 0 ]; then
            LOGE "设置 Let’s Encrypt 为默认 CA 失败，退出脚本…"
            exit 1
        fi
        export CF_Key="${CF_GlobalKey}"
        export CF_Email=${CF_AccountEmail}
        ~/.acme.sh/acme.sh --issue --dns dns_cf -d ${CF_Domain} -d *.${CF_Domain} --log
        if [ $? -ne 0 ]; then
            LOGE "证书申请失败，退出脚本…"
            exit 1
        else
            LOGI "证书申请成功，正在安装…"
        fi
        ~/.acme.sh/acme.sh --installcert -d ${CF_Domain} -d *.${CF_Domain} --ca-file /root/cert/ca.cer \
            --cert-file /root/cert/${CF_Domain}.cer --key-file /root/cert/${CF_Domain}.key \
            --fullchain-file /root/cert/fullchain.cer
        if [ $? -ne 0 ]; then
            LOGE "证书安装失败，退出脚本…"
            exit 1
        else
            LOGI "证书安装成功，正在启用自动更新…"
        fi
        ~/.acme.sh/acme.sh --upgrade --auto-upgrade
        if [ $? -ne 0 ]; then
            LOGE "自动更新设置失败，退出脚本…"
            ls -lah "$certPath"
            chmod 755 "$certPath"
            exit 1
        else
            LOGI "证书已安装并开启自动续期，详细信息如下"
            ls -lah "$certPath"
            chmod 755 "$certPath"
        fi
    else
        show_menu
    fi
}

firewall_menu() {
    echo -e "${green}\t1.${plain} 安装防火墙并开放端口"
    echo -e "${green}\t2.${plain} 查看已放行规则"
    echo -e "${green}\t3.${plain} 删除端口规则"
    echo -e "${green}\t4.${plain} 关闭防火墙"
    echo -e "${green}\t0.${plain} 返回主菜单"
    read -p "请选择：" choice
    case "$choice" in
    0)
        show_menu
        ;;
    1)
        open_ports
        ;;
    2)
        sudo ufw status
        ;;
    3)
        delete_ports
        ;;
    4)
        sudo ufw disable
        ;;
    *) echo "无效的选项" ;;
    esac
}

open_ports() {
    if ! command -v ufw &>/dev/null; then
        echo "ufw firewall is not installed. Installing now..."
        apt-get update
        apt-get install -y ufw
    else
        echo "ufw firewall is already installed"
    fi

    # Check if the firewall is inactive
    if ufw status | grep -q "Status: active"; then
        echo "firewall is already active"
    else
        # Open the necessary ports
        ufw allow ssh
        ufw allow http
        ufw allow https
        ufw allow 54321/tcp

        # Enable the firewall
        ufw --force enable
    fi

    # Prompt the user to enter a list of ports
    read -p "Enter the ports you want to open (e.g. 80,443,2053 or range 400-500): " ports

    # Check if the input is valid
    if ! [[ $ports =~ ^([0-9]+|[0-9]+-[0-9]+)(,([0-9]+|[0-9]+-[0-9]+))*$ ]]; then
        echo "Error: Invalid input. Please enter a comma-separated list of ports or a range of ports (e.g. 80,443,2053 or 400-500)." >&2
        exit 1
    fi

    # Open the specified ports using ufw
    IFS=',' read -ra PORT_LIST <<<"$ports"
    for port in "${PORT_LIST[@]}"; do
        if [[ $port == *-* ]]; then
            # Split the range into start and end ports
            start_port=$(echo $port | cut -d'-' -f1)
            end_port=$(echo $port | cut -d'-' -f2)
            # Loop through the range and open each port
            for ((i = start_port; i <= end_port; i++)); do
                ufw allow $i
            done
        else
            ufw allow "$port"
        fi
    done

    # Confirm that the ports are open
    ufw status | grep $ports
}

delete_ports() {
    # Prompt the user to enter the ports they want to delete
    read -p "Enter the ports you want to delete (e.g. 80,443,2053 or range 400-500): " ports

    # Check if the input is valid
    if ! [[ $ports =~ ^([0-9]+|[0-9]+-[0-9]+)(,([0-9]+|[0-9]+-[0-9]+))*$ ]]; then
        echo "Error: Invalid input. Please enter a comma-separated list of ports or a range of ports (e.g. 80,443,2053 or 400-500)." >&2
        exit 1
    fi

    # Delete the specified ports using ufw
    IFS=',' read -ra PORT_LIST <<<"$ports"
    for port in "${PORT_LIST[@]}"; do
        if [[ $port == *-* ]]; then
            # Split the range into start and end ports
            start_port=$(echo $port | cut -d'-' -f1)
            end_port=$(echo $port | cut -d'-' -f2)
            # Loop through the range and delete each port
            for ((i = start_port; i <= end_port; i++)); do
                ufw delete allow $i
            done
        else
            ufw delete allow "$port"
        fi
    done

    # Confirm that the ports are deleted
    echo "Deleted the specified ports:"
    ufw status | grep $ports
}

bbr_menu() {
    echo -e "${green}\t1.${plain} 启用 BBR"
    echo -e "${green}\t2.${plain} 关闭 BBR"
    echo -e "${green}\t0.${plain} 返回主菜单"
    read -p "请选择：" choice
    case "$choice" in
    0)
        show_menu
        ;;
    1)
        enable_bbr
        ;;
    2)
        disable_bbr
        ;;
    *) echo "无效的选项" ;;
    esac
}

disable_bbr() {

    if ! grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf || ! grep -q "net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf; then
        echo -e "${yellow}BBR is not currently enabled.${plain}"
        exit 0
    fi

    # Replace BBR with CUBIC configurations
    sed -i 's/net.core.default_qdisc=fq/net.core.default_qdisc=pfifo_fast/' /etc/sysctl.conf
    sed -i 's/net.ipv4.tcp_congestion_control=bbr/net.ipv4.tcp_congestion_control=cubic/' /etc/sysctl.conf

    # Apply changes
    sysctl -p

    # Verify that BBR is replaced with CUBIC
    if [[ $(sysctl net.ipv4.tcp_congestion_control | awk '{print $3}') == "cubic" ]]; then
        echo -e "${green}BBR has been replaced with CUBIC successfully.${plain}"
    else
        echo -e "${red}Failed to replace BBR with CUBIC. Please check your system configuration.${plain}"
    fi
}

enable_bbr() {
    if grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf && grep -q "net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf; then
        echo -e "${green}BBR is already enabled!${plain}"
        exit 0
    fi

    # Check the OS and install necessary packages
    case "${release}" in
    ubuntu | debian | armbian)
        apt-get update && apt-get install -yqq --no-install-recommends ca-certificates
        ;;
    centos | almalinux | rocky | ol)
        yum -y update && yum -y install ca-certificates
        ;;
    fedora | amzn)
        dnf -y update && dnf -y install ca-certificates
        ;;
    arch | manjaro | parch)
        pacman -Sy --noconfirm ca-certificates
        ;;
    *)
        echo -e "${red}Unsupported operating system. Please check the script and install the necessary packages manually.${plain}\n"
        exit 1
        ;;
    esac

    # Enable BBR
    echo "net.core.default_qdisc=fq" | tee -a /etc/sysctl.conf
    echo "net.ipv4.tcp_congestion_control=bbr" | tee -a /etc/sysctl.conf

    # Apply changes
    sysctl -p

    # Verify that BBR is enabled
    if [[ $(sysctl net.ipv4.tcp_congestion_control | awk '{print $3}') == "bbr" ]]; then
        echo -e "${green}BBR has been enabled successfully.${plain}"
    else
        echo -e "${red}Failed to enable BBR. Please check your system configuration.${plain}"
    fi
}

update_geo() {
    cd /usr/local/x-ui/bin
    echo -e "${green}\t1.${plain} 更新地理数据 [推荐] "
    echo -e "${green}\t2.${plain} 从 jsDelivr CDN 下载 "
    echo -e "${green}\t0.${plain} 返回主菜单 "
    read -p "请选择：" select

    case "$select" in
    0)
        show_menu
        ;;

    1)
        wget -N "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat" && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget -N "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat" && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget "https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat" -O /tmp/wget && mv /tmp/wget geoip_IR.dat && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget "https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat" -O /tmp/wget && mv /tmp/wget geosite_IR.dat && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        echo -e "${green}Files are updated.${plain}"
        confirm_restart
        ;;

    2)
        wget -N "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geoip.dat" && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget -N "https://cdn.jsdelivr.net/gh/Loyalsoldier/v2ray-rules-dat@release/geosite.dat" && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget "https://cdn.jsdelivr.net/gh/chocolate4u/Iran-v2ray-rules@release/geoip.dat" -O /tmp/wget && mv /tmp/wget geoip_IR.dat && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        wget "https://cdn.jsdelivr.net/gh/chocolate4u/Iran-v2ray-rules@release/geosite.dat" -O /tmp/wget && mv /tmp/wget geosite_IR.dat && echo -e "${green}Success${plain}\n" || echo -e "${red}Failure${plain}\n"
        echo -e "${green}Files are updated.${plain}"
        confirm_restart
        ;;

    *)
        LOGE "Please enter a correct number [0-2]\n"
        update_geo
        ;;
    esac
}

run_speedtest() {
    # Check if Speedtest is already installed
    if ! command -v speedtest &>/dev/null; then
        # If not installed, install it
        local pkg_manager=""
        local speedtest_install_script=""

        if command -v dnf &>/dev/null; then
            pkg_manager="dnf"
            speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh"
        elif command -v yum &>/dev/null; then
            pkg_manager="yum"
            speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.rpm.sh"
        elif command -v apt-get &>/dev/null; then
            pkg_manager="apt-get"
            speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh"
        elif command -v apt &>/dev/null; then
            pkg_manager="apt"
            speedtest_install_script="https://packagecloud.io/install/repositories/ookla/speedtest-cli/script.deb.sh"
        fi

        if [[ -z $pkg_manager ]]; then
            echo "Error: Package manager not found. You may need to install Speedtest manually."
            return 1
        else
            curl -s $speedtest_install_script | bash
            $pkg_manager install -y speedtest
        fi
    fi

    # Run Speedtest
    speedtest
}

# Install through Cloudflare's signed APT repository. This leaves the panel's
# managed tunnel processes alone; they need to be restarted after an upgrade.
cloudflared_package() (
    local action=$1 current package_status key_tmp source_tmp
    if ! command -v apt-get >/dev/null || ! command -v dpkg-query >/dev/null; then
        LOGE "此菜单目前支持 Debian 和 Ubuntu 的 APT 安装；其他系统请使用 Cloudflare 官方安装说明。"
        return 1
    fi
    if ! command -v curl >/dev/null; then
        LOGE "未找到 curl，请先安装 curl 和 CA 证书。"
        return 1
    fi
    package_status=$(dpkg-query -W -f='${Status}' cloudflared 2>/dev/null || true)
    current=$(command -v cloudflared || true)
    if [[ -n "$current" ]] && ! dpkg-query -S "$(readlink -f "$current")" 2>/dev/null | grep -q '^cloudflared:'; then
        LOGE "检测到非 APT 管理的 cloudflared：$current；请先检查现有安装。"
        return 1
    fi
    if [[ "$action" == update && "$package_status" != "install ok installed" ]]; then
        LOGE "尚未通过 APT 安装 cloudflared，请先选择安装。"
        return 1
    fi
    if [[ -e /etc/apt/sources.list.d/cloudflared.list ]] && ! grep -Fxq 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' /etc/apt/sources.list.d/cloudflared.list; then
        LOGE "已有不同的 Cloudflare 软件源配置，请先检查。"
        return 1
    fi
    mkdir -p /usr/share/keyrings /etc/apt/sources.list.d || return 1
    if [[ ! -s /usr/share/keyrings/cloudflare-main.gpg ]]; then
        key_tmp=$(mktemp /usr/share/keyrings/.cloudflare-main.XXXXXX) || return 1
        if ! curl -fsSL --connect-timeout 15 --max-time 120 https://pkg.cloudflare.com/cloudflare-main.gpg -o "$key_tmp" || [[ ! -s "$key_tmp" ]]; then
            rm -f -- "$key_tmp"
            LOGE "下载 Cloudflare 软件源密钥失败。"
            return 1
        fi
        chmod 0644 "$key_tmp" && mv -f -- "$key_tmp" /usr/share/keyrings/cloudflare-main.gpg || return 1
    fi
    if [[ ! -e /etc/apt/sources.list.d/cloudflared.list ]]; then
        source_tmp=$(mktemp /etc/apt/sources.list.d/.cloudflared.XXXXXX) || return 1
        printf '%s\n' 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' > "$source_tmp" && chmod 0644 "$source_tmp" && mv -f -- "$source_tmp" /etc/apt/sources.list.d/cloudflared.list || return 1
    fi
    LOGI "正在更新软件源，请稍候……"
    if ! apt-get -o APT::Update::Error-Mode=any update; then LOGE "软件源更新失败，未安装或更新 cloudflared。"; return 1; fi
    if [[ "$action" == update ]]; then
        apt-get install -y --only-upgrade cloudflared || { LOGE "cloudflared 更新失败。"; return 1; }
    else
        apt-get install -y cloudflared || { LOGE "cloudflared 安装失败。"; return 1; }
    fi
    if ! command -v cloudflared >/dev/null || ! cloudflared --version; then LOGE "无法运行 cloudflared，请检查安装。"; return 1; fi
    LOGI "如隧道正在运行，请在面板中停止并重新启动隧道以使用新版本。"
)

cloudflared_menu() {
    echo "1. 安装最新版 cloudflared"
    echo "2. 更新 cloudflared 到最新版"
    echo "0. 返回主菜单"
    local choice
    read -r -p "请选择：" choice
    case "$choice" in
        1) cloudflared_package install ;;
        2) cloudflared_package update ;;
        0) show_menu; return ;;
        *) LOGE "无效的选项" ;;
    esac
    before_show_menu
}

show_usage() {
    echo "面板管理命令帮助"
    echo "------------------------------------------"
    echo "可用命令："
    echo "x-ui              - 面板管理脚本"
    echo "x-ui start        - 启动面板"
    echo "x-ui stop         - 停止面板"
    echo "x-ui restart      - 重启面板"
    echo "x-ui restart-xray - 重启 Xray"
    echo "x-ui status       - 查看状态"
    echo "x-ui settings     - 查看设置"
    echo "x-ui enable       - 启用开机自启"
    echo "x-ui disable      - 关闭开机自启"
    echo "x-ui log          - 查看日志"
    echo "x-ui update PACKAGE_DIR           - 从本地安装包升级"
    echo "x-ui install PACKAGE_DIR [PORT]   - 从本地安装包安装"
    echo "x-ui recover PACKAGE_DIR          - 恢复中断的安装或升级"
    echo "x-ui uninstall    - 卸载面板"
    echo "x-ui help         - 查看帮助"
    echo "------------------------------------------"
}

show_menu() {
    echo -e "
  ${green}面板管理菜单 ${plain}
————————————————
  ${green}0.${plain} 退出 
————————————————
  ${green}1.${plain} 从本地安装包安装
  ${green}2.${plain} 从本地安装包升级
  ${green}4.${plain} 卸载面板
————————————————
  ${green}5.${plain} 重置用户名和密码
  ${green}6.${plain} 重置面板访问路径
  ${green}7.${plain} 重置面板设置
  ${green}8.${plain} 设置面板端口
  ${green}9.${plain} 查看面板设置
————————————————
  ${green}10.${plain} 启动面板
  ${green}11.${plain} 停止面板
  ${green}12.${plain} 重启面板
  ${green}13.${plain} 重启 Xray
  ${green}14.${plain} 查看状态
  ${green}15.${plain} 查看日志
————————————————
  ${green}16.${plain} 启用开机自启
  ${green}17.${plain} 关闭开机自启
————————————————
  ${green}18.${plain} SSL 证书管理
  ${green}19.${plain} Cloudflare SSL 证书
  ${green}20.${plain} 防火墙管理
————————————————
  ${green}21.${plain} 启用或关闭 BBR
  ${green}22.${plain} 更新地理数据
  ${green}23.${plain} Ookla 网络测速
  ${green}24.${plain} Cloudflare 隧道程序管理
 "
    show_status
    echo && read -p "请输入选项 [0-24]：" num

    case "${num}" in
    0)
        exit 0
        ;;
    1)
        check_uninstall && install
        ;;
    2)
        check_install && update
        ;;
    4)
        check_install && uninstall
        ;;
    5)
        check_install && reset_user
        ;;
    6)
        check_install && reset_webbasepath
        ;;
    7)
        check_install && reset_config
        ;;
    8)
        check_install && set_port
        ;;
    9)
        check_install && check_config && get_uri
        ;;
    10)
        check_install && start
        ;;
    11)
        check_install && stop
        ;;
    12)
        check_install && restart
        ;;
    13)
        check_install && restart_xray
        ;;
    14)
        check_install && status
        ;;
    15)
        check_install && show_log
        ;;
    16)
        check_install && enable
        ;;
    17)
        check_install && disable
        ;;
    18)
        ssl_cert_issue_main
        ;;
    19)
        ssl_cert_issue_CF
        ;;
    20)
        firewall_menu
        ;;
    21)
        bbr_menu
        ;;
    22)
        update_geo
        ;;
    23)
        run_speedtest
        ;;
    24)
        cloudflared_menu
        ;;
    *)
        LOGE "请输入正确的数字 [0-24]"
        ;;
    esac
}

if [[ $# > 0 ]]; then
    case $1 in
    "start")
        check_install 0 && start 0
        ;;
    "stop")
        check_install 0 && stop 0
        ;;
    "restart")
        check_install 0 && restart 0
        ;;
    "restart-xray")
        check_install 0 && restart_xray 0
        ;;
    "status")
        check_install 0 && status 0
        ;;
    "settings")
        check_install 0 && check_config 0 && get_uri 0
        ;;
    "enable")
        check_install 0 && enable 0
        ;;
    "disable")
        check_install 0 && disable 0
        ;;
    "log")
        check_install 0 && show_log 0
        ;;
    "update")
        if [[ $# -gt 2 ]]; then LOGE "Usage: x-ui update PACKAGE_DIR"; exit 2; fi
        check_install 0 && update "${2:-}"
        ;;
    "install")
        if [[ $# -gt 3 ]]; then LOGE "Usage: x-ui install PACKAGE_DIR [PORT]"; exit 2; fi
        check_uninstall 0 && install "${2:-}" "${3:-}"
        ;;
    "recover")
        if [[ $# -gt 2 ]]; then LOGE "Usage: x-ui recover PACKAGE_DIR"; exit 2; fi
        recover "${2:-}"
        ;;
    "uninstall")
        check_install 0 && uninstall 0
        ;;
    *) show_usage ;;
    esac
else
    show_menu
fi
