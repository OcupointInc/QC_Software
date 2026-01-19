#!/bin/bash

# --- Configuration ---
# The PCIe Domain we are looking for (0005 for C5)
TARGET_DOMAIN_ID=5
# The Driver Name (usually tegra194-pcie even on Orin)
DRIVER_PATH=$(find /sys/bus/platform/drivers -name "tegra194-pcie" -o -name "tegra234-pcie" | head -n 1)

echo "--- Starting Platform-Level PCIe Recovery ---"

if [ -z "$DRIVER_PATH" ]; then
    echo "ERROR: Could not find Tegra PCIe driver in sysfs."
    exit 1
fi
echo "Found Driver Path: $DRIVER_PATH"

# 1. Find the Hardware Address for Domain 0005
# We scan the Device Tree to match the PCIe domain ID to the controller address (e.g., 141a0000.pcie)
echo "[1/4] Identifying Controller for Domain $TARGET_DOMAIN_ID..."
TARGET_CONTROLLER=""

for dt_node in /proc/device-tree/pcie@*; do
    if [ -f "$dt_node/linux,pci-domain" ]; then
        # Read the domain ID (Big Endian 32-bit integer)
        # We use hexdump to extract it safely
        DOMAIN=$(hexdump -e '1/4 "%u"' "$dt_node/linux,pci-domain")
        
        # Check if this matches our target (handle endianness swap if needed, but usually 5 matches 5)
        # Note: hexdump might print big-endian as a large number on LE systems, 
        # so we also check the raw hex just in case, or simply check the node name mapping for C5.
        
        # Orin C5 is almost always at 141a0000. Let's strictly check the address if domain parsing is tricky in bash.
        # But let's try a simpler mapping for Orin:
        # C5 (0005) -> 141a0000.pcie
        case "$dt_node" in
            *"141a0000"*)
                echo "      Found C5 Controller candidate: $dt_node"
                TARGET_CONTROLLER="141a0000.pcie"
                ;;
        esac
    fi
done

# Fallback: If we couldn't auto-detect, force C5 address
if [ -z "$TARGET_CONTROLLER" ]; then
    echo "      Warning: Auto-detect failed. Assuming default AGX Orin C5 address."
    TARGET_CONTROLLER="141a0000.pcie"
fi

echo "      Targeting Controller: $TARGET_CONTROLLER"

# 2. Unbind the Controller (Force Power Down)
if [ -e "$DRIVER_PATH/$TARGET_CONTROLLER" ]; then
    echo "[2/4] Unbinding controller to reset hardware..."
    echo "$TARGET_CONTROLLER" | sudo tee "$DRIVER_PATH/unbind" > /dev/null
    sleep 1
else
    echo "[2/4] Controller was already unbound (powered off)."
fi

# 3. Bind the Controller (Force Power Up & Link Training)
echo "[3/4] Binding controller (This triggers Link Training)..."
# Ensure the FPGA is powered ON before this step!
echo "      (Ensure FPGA is powered ON now)"
sleep 1

echo "$TARGET_CONTROLLER" | sudo tee "$DRIVER_PATH/bind" > /dev/null

# 4. Verify
echo "[4/4] Verifying Link..."
sleep 2

if lspci | grep -q "0005:00:00.0"; then
    echo "SUCCESS: Bridge 0005:00:00.0 is back!"
    
    # Now load the driver if the device is seen
    if lspci -s "0005:01:00.0" | grep -q "Xilinx"; then
        echo "SUCCESS: FPGA Detected. Loading XDMA..."
        sudo modprobe xdma
    else
        echo "WARNING: Bridge is up, but FPGA endpoint not seen. Rescanning bus..."
        echo 1 | sudo tee /sys/bus/pci/rescan > /dev/null
        sleep 1
        lspci -s "0005:01:00.0"
    fi
else
    echo "ERROR: Controller failed to bind or link did not train."
    echo "       Check dmesg for 'phy link never came up'."
fi

echo "--- Complete ---"