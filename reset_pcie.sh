#!/bin/bash

# Configuration
PCI_ADDR="0005:01:00.0"
DRIVER_NAME="xdma"

echo "--- Starting XDMA Recovery Sequence ---"

# 1. Remove the driver if it is currently loaded
if lsmod | grep -q "$DRIVER_NAME"; then
    echo "[1/4] Removing existing $DRIVER_NAME driver..."
    sudo rmmod $DRIVER_NAME
else
    echo "[1/4] Driver $DRIVER_NAME not loaded, skipping rmmod."
fi

# 2. Tell the kernel to forget the device
if [ -e "/sys/bus/pci/devices/$PCI_ADDR" ]; then
    echo "[2/4] Removing PCI device $PCI_ADDR from kernel tree..."
    echo 1 | sudo tee "/sys/bus/pci/devices/$PCI_ADDR/remove" > /dev/null
else
    echo "[2/4] Device $PCI_ADDR not found in sysfs, skipping remove."
fi

# 3. Rescan the bus
echo "[3/4] Rescanning PCIe bus (waiting for FPGA to respond)..."
sleep 1
echo 1 | sudo tee /sys/bus/pci/rescan > /dev/null

# 4. Final verification and driver loading
if lspci -s "$PCI_ADDR" | grep -q "Xilinx"; then
    echo "[4/4] Device detected via lspci. Loading driver..."
    sudo modprobe $DRIVER_NAME

    # Check if /dev nodes were created
    sleep 1
    if ls /dev/xdma* >/dev/null 2>&1; then
        echo "SUCCESS: XDMA nodes are now available:"
        ls -l /dev/xdma*
    else
        echo "ERROR: Device found, but driver failed to create /dev nodes."
        echo "Check 'dmesg | tail' for 'Failed to detect XDMA config BAR'."
    fi
else
    echo "ERROR: Device $PCI_ADDR not found after rescan."
    echo "Check FPGA power and bitstream status."
fi

echo "--- Sequence Complete ---"