package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

var (
	errNoWrite                   = errors.New("bluetooth: write not supported")
	errNoWriteWithoutResponse    = errors.New("bluetooth: write without response not supported")
	errWriteFailed               = errors.New("bluetooth: write failed")
	errNoRead                    = errors.New("bluetooth: read not supported")
	errNoNotify                  = errors.New("bluetooth: notify not supported")
	errNoIndicate                = errors.New("bluetooth: indicate not supported")
	errNoNotifyOrIndicate        = errors.New("bluetooth: notify or indicate not supported")
	errInvalidNotificationMode   = errors.New("bluetooth: invalid notification mode")
	errEnableNotificationsFailed = errors.New("bluetooth: enable notifications failed")
)

type NotificationMode = genericattributeprofile.GattCharacteristicProperties

const (
	NotificationModeNotify   NotificationMode = genericattributeprofile.GattCharacteristicPropertiesNotify
	NotificationModeIndicate NotificationMode = genericattributeprofile.GattCharacteristicPropertiesIndicate
)

// DiscoverServices starts a service discovery procedure. Pass a list of service
// UUIDs you are interested in to this function. Either a slice of all services
// is returned (of the same length as the requested UUIDs and in the same
// order), or if some services could not be discovered an error is returned.
//
// Passing a nil slice of UUIDs will return a complete list of
// services.
func (d Device) DiscoverServices(filterUUIDs []UUID) ([]DeviceService, error) {
	return d.DiscoverServicesWithContext(context.Background(), filterUUIDs)
}

// DiscoverServicesWithContext starts a service discovery procedure. Pass a list of service
// UUIDs you are interested in to this function. Either a slice of all services
// is returned (of the same length as the requested UUIDs and in the same
// order), or if some services could not be discovered an error is returned.
//
// Passing a nil slice of UUIDs will return a complete list of
// services.
func (d Device) DiscoverServicesWithContext(ctx context.Context, filterUUIDs []UUID) ([]DeviceService, error) {
	// IAsyncOperation<GattDeviceServicesResult>
	getServicesOperation, err := d.device.GetGattServicesWithCacheModeAsync(bluetooth.BluetoothCacheModeUncached)
	if err != nil {
		return nil, err
	}

	if err := awaitAsyncOperation(ctx, getServicesOperation, genericattributeprofile.SignatureGattDeviceServicesResult); err != nil {
		return nil, err
	}

	res, err := getServicesOperation.GetResults()
	if err != nil {
		return nil, err
	}

	servicesResult := (*genericattributeprofile.GattDeviceServicesResult)(res)

	status, err := servicesResult.GetStatus()
	if err != nil {
		return nil, err
	} else if status != genericattributeprofile.GattCommunicationStatusSuccess {
		return nil, fmt.Errorf("could not retrieve device services, operation failed with code %d", status)
	}

	// IVectorView<GattDeviceService>
	servicesVector, err := servicesResult.GetServices()
	if err != nil {
		return nil, err
	}

	// Convert services vector to array
	servicesSize, err := servicesVector.GetSize()
	if err != nil {
		return nil, err
	}

	var services []DeviceService
	for i := uint32(0); i < servicesSize; i++ {
		s, err := servicesVector.GetAt(i)
		if err != nil {
			return nil, err
		}

		srv := (*genericattributeprofile.GattDeviceService)(s)
		guid, err := srv.GetUuid()
		if err != nil {
			return nil, err
		}

		serviceUuid := winRTUuidToUuid(guid)

		// only include services that are included in the input filter
		if len(filterUUIDs) > 0 {
			found := false
			for _, uuid := range filterUUIDs {
				if serviceUuid.String() == uuid.String() {
					// One of the services we're looking for.
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		go func() {
			<-d.ctx.Done()
			srv.Close()
		}()

		services = append(services, DeviceService{
			uuidWrapper: serviceUuid,
			service:     srv,
			device:      d,
		})
	}

	return services, nil
}

func winRTUuidToUuid(uuid syscall.GUID) UUID {
	return NewUUID([16]byte{
		byte(uuid.Data1 >> 24),
		byte(uuid.Data1 >> 16),
		byte(uuid.Data1 >> 8),
		byte(uuid.Data1),
		byte(uuid.Data2 >> 8),
		byte(uuid.Data2),
		byte(uuid.Data3 >> 8),
		byte(uuid.Data3),
		uuid.Data4[0], uuid.Data4[1],
		uuid.Data4[2], uuid.Data4[3],
		uuid.Data4[4], uuid.Data4[5],
		uuid.Data4[6], uuid.Data4[7],
	})
}

// uuidWrapper is a type alias for UUID so we ensure no conflicts with
// struct method of the same name.
type uuidWrapper = UUID

// DeviceService is a BLE service on a connected peripheral device.
type DeviceService struct {
	uuidWrapper

	service *genericattributeprofile.GattDeviceService
	device  Device
}

// UUID returns the UUID for this DeviceService.
func (s DeviceService) UUID() UUID {
	return s.uuidWrapper
}

// DiscoverCharacteristics discovers characteristics in this service. Pass a
// list of characteristic UUIDs you are interested in to this function. Either a
// list of all requested characteristics is returned, or if some characteristics could not be
// discovered an error is returned. If there is no error, the characteristics
// slice has the same length as the UUID slice with characteristics in the same
// order in the slice as in the requested UUID list.
//
// Passing a nil slice of UUIDs will return a complete
// list of characteristics.
func (s DeviceService) DiscoverCharacteristics(filterUUIDs []UUID) ([]DeviceCharacteristic, error) {
	return s.DiscoverCharacteristicsWithContext(context.Background(), filterUUIDs)
}

// DiscoverCharacteristicsWithContext discovers characteristics in this service. Pass a
// list of characteristic UUIDs you are interested in to this function. Either a
// list of all requested characteristics is returned, or if some characteristics could not be
// discovered an error is returned. If there is no error, the characteristics
// slice has the same length as the UUID slice with characteristics in the same
// order in the slice as in the requested UUID list.
//
// Passing a nil slice of UUIDs will return a complete
// list of characteristics.
func (s DeviceService) DiscoverCharacteristicsWithContext(ctx context.Context, filterUUIDs []UUID) ([]DeviceCharacteristic, error) {
	getCharacteristicsOp, err := s.service.GetCharacteristicsWithCacheModeAsync(bluetooth.BluetoothCacheModeUncached)
	if err != nil {
		return nil, err
	}

	// IAsyncOperation<GattCharacteristicsResult>
	if err := awaitAsyncOperation(ctx, getCharacteristicsOp, genericattributeprofile.SignatureGattCharacteristicsResult); err != nil {
		return nil, err
	}

	res, err := getCharacteristicsOp.GetResults()
	if err != nil {
		return nil, err
	}

	gattCharResult := (*genericattributeprofile.GattCharacteristicsResult)(res)

	// IVectorView<GattCharacteristic>
	charVector, err := gattCharResult.GetCharacteristics()
	if err != nil {
		return nil, err
	}

	// Convert characteristics vector to array
	characteristicsSize, err := charVector.GetSize()
	if err != nil {
		return nil, err
	}

	var characteristics []DeviceCharacteristic

	if len(filterUUIDs) > 0 {
		// The caller wants to get a list of characteristics in a specific
		// order.
		characteristics = make([]DeviceCharacteristic, len(filterUUIDs))
	}

	for i := uint32(0); i < characteristicsSize; i++ {
		c, err := charVector.GetAt(i)
		if err != nil {
			return nil, err
		}

		characteristic := (*genericattributeprofile.GattCharacteristic)(c)
		guid, err := characteristic.GetUuid()
		if err != nil {
			return nil, err
		}

		characteristicUUID := winRTUuidToUuid(guid)

		properties, err := characteristic.GetCharacteristicProperties()
		if err != nil {
			return nil, err
		}

		// only include characteristics that are included in the input filter
		if len(filterUUIDs) > 0 {
			for j, uuid := range filterUUIDs {
				if characteristics[j] != (DeviceCharacteristic{}) {
					// To support multiple identical characteristics, we
					// need to ignore the characteristics that are already
					// found. See:
					// https://github.com/tinygo-org/bluetooth/issues/131
					continue
				}
				if characteristicUUID.String() == uuid.String() {
					// One of the characteristics we're looking for.
					characteristics[j] = s.makeCharacteristic(characteristicUUID, characteristic, properties)
					break
				}
			}
		} else {
			// The caller wants to get all characteristics, in any order.
			characteristics = append(characteristics, s.makeCharacteristic(characteristicUUID, characteristic, properties))
		}
	}

	if slices.Contains(characteristics, (DeviceCharacteristic{})) {
		return nil, errors.New("bluetooth: did not find all requested characteristic")
	}

	return characteristics, nil
}

// Small helper to create a DeviceCharacteristic object.
func (s DeviceService) makeCharacteristic(uuid UUID, characteristic *genericattributeprofile.GattCharacteristic, properties genericattributeprofile.GattCharacteristicProperties) DeviceCharacteristic {
	char := DeviceCharacteristic{
		deviceCharacteristic: &deviceCharacteristic{
			uuidWrapper:    uuid,
			service:        s,
			characteristic: characteristic,
			properties:     properties,
		},
	}
	return char
}

// DeviceCharacteristic is a BLE characteristic on a connected peripheral
// device.
type DeviceCharacteristic struct {
	*deviceCharacteristic
}

type deviceCharacteristic struct {
	uuidWrapper

	characteristic *genericattributeprofile.GattCharacteristic
	properties     genericattributeprofile.GattCharacteristicProperties

	service DeviceService
}

// UUID returns the UUID for this DeviceCharacteristic.
func (c DeviceCharacteristic) UUID() UUID {
	return c.uuidWrapper
}

func (c DeviceCharacteristic) Properties() uint32 {
	return uint32(c.properties)
}

// GetMTU returns the MTU for the characteristic.
func (c DeviceCharacteristic) GetMTU() (uint16, error) {
	return c.service.device.session.GetMaxPduSize()
}

// Value reads the current in-memory value of the characteristic.
// This retrieves the cached value without performing a new read operation.
func (c DeviceCharacteristic) Value(data []byte) (n int, err error) {
	// Read the cached value using ReadValueWithCacheMode with Cached mode
	readOp, err := c.characteristic.ReadValueWithCacheModeAsync(bluetooth.BluetoothCacheModeCached)
	if err != nil {
		return 0, err
	}

	// IAsyncOperation<GattReadResult>
	if err := awaitAsyncOperation(context.Background(), readOp, genericattributeprofile.SignatureGattReadResult); err != nil {
		return 0, err
	}

	res, err := readOp.GetResults()
	if err != nil {
		return 0, err
	}

	result := (*genericattributeprofile.GattReadResult)(res)

	buffer, err := result.GetValue()
	if err != nil {
		return 0, err
	}

	if buffer == nil {
		return 0, nil
	}

	datareader, err := streams.DataReaderFromBuffer(buffer)
	if err != nil {
		return 0, err
	}
	defer datareader.Release()

	bufferlen, err := buffer.GetLength()
	if err != nil {
		return 0, err
	}

	if bufferlen == 0 {
		return 0, nil
	}

	readBuffer, err := datareader.ReadBytes(bufferlen)
	if err != nil {
		return 0, err
	}

	copy(data, readBuffer)
	return len(readBuffer), nil
}

// CanSendWriteWithoutResponse returns whether a WriteWithoutResponse can be sent
// at this time. If this returns false, you must wait for some time before
// sending another WriteWithoutResponse. This is typically because the internal
// buffer is full. You can use this to implement your own flow control when
// sending many WriteWithoutResponse calls in a row.
func (c DeviceCharacteristic) CanSendWriteWithoutResponse() bool {
	// Windows WinRT doesn't expose buffer status directly, so we conservatively
	// return true. The underlying WinRT implementation handles flow control.
	return true
}

// Write replaces the characteristic value with a new value. The
// call will return after all data has been written.
func (c DeviceCharacteristic) Write(p []byte) (n int, err error) {
	return c.WriteWithContext(context.Background(), p)
}

// Write replaces the characteristic value with a new value. The
// call will return after all data has been written.
func (c DeviceCharacteristic) WriteWithContext(ctx context.Context, p []byte) (n int, err error) {
	if c.properties&genericattributeprofile.GattCharacteristicPropertiesWrite == 0 {
		return 0, errNoWrite
	}

	return c.write(ctx, p, genericattributeprofile.GattWriteOptionWriteWithResponse)
}

func (c DeviceCharacteristic) WriteWithoutResponse(p []byte) (n int, err error) {
	return c.WriteWithoutResponseWithContext(context.Background(), p)
}

// WriteWithoutResponse replaces the characteristic value with a new value. The
// call will return before all data has been written. A limited number of such
// writes can be in flight at any given time. This call is also known as a
// "write command" (as opposed to a write request).
func (c DeviceCharacteristic) WriteWithoutResponseWithContext(ctx context.Context, p []byte) (n int, err error) {
	if c.properties&genericattributeprofile.GattCharacteristicPropertiesWriteWithoutResponse == 0 {
		return 0, errNoWriteWithoutResponse
	}
	return c.write(ctx, p, genericattributeprofile.GattWriteOptionWriteWithoutResponse)
}

func (c DeviceCharacteristic) write(ctx context.Context, p []byte, mode genericattributeprofile.GattWriteOption) (n int, err error) {
	// Convert data to buffer
	writer, err := streams.NewDataWriter()
	if err != nil {
		return 0, err
	}
	defer writer.Release()

	// Add bytes to writer
	if err := writer.WriteBytes(uint32(len(p)), p); err != nil {
		return 0, err
	}

	value, err := writer.DetachBuffer()
	if err != nil {
		return 0, err
	}

	// IAsyncOperation<GattCommunicationStatus>
	asyncOp, err := c.characteristic.WriteValueWithOptionAsync(value, mode)

	if err := awaitAsyncOperation(ctx, asyncOp, genericattributeprofile.SignatureGattCommunicationStatus); err != nil {
		return 0, err
	}

	res, err := asyncOp.GetResults()
	if err != nil {
		return 0, err
	}

	status := genericattributeprofile.GattCommunicationStatus(uintptr(res))

	// Is the status success?
	if status != genericattributeprofile.GattCommunicationStatusSuccess {
		return 0, errWriteFailed
	}

	// Success
	return len(p), nil
}

// Read reads the current characteristic value.
func (c DeviceCharacteristic) Read(data []byte) (n int, err error) {
	ctx, cancel := context.WithTimeoutCause(context.Background(), 10*time.Second, errors.New("timeout on Read()"))
	defer cancel()
	return c.ReadWithContext(ctx, data)
}

// Read reads the current characteristic value.
func (c DeviceCharacteristic) ReadWithContext(ctx context.Context, data []byte) (int, error) {
	if c.properties&genericattributeprofile.GattCharacteristicPropertiesRead == 0 {
		return 0, errNoRead
	}

	readOp, err := c.characteristic.ReadValueWithCacheModeAsync(bluetooth.BluetoothCacheModeUncached)
	if err != nil {
		return 0, err
	}

	// IAsyncOperation<GattReadResult>
	if err := awaitAsyncOperation(ctx, readOp, genericattributeprofile.SignatureGattReadResult); err != nil {
		return 0, err
	}

	res, err := readOp.GetResults()
	if err != nil {
		return 0, err
	}

	result := (*genericattributeprofile.GattReadResult)(res)

	buffer, err := result.GetValue()
	if err != nil {
		return 0, err
	}

	datareader, err := streams.DataReaderFromBuffer(buffer)
	if err != nil {
		return 0, err
	}

	bufferlen, err := buffer.GetLength()
	if err != nil {
		return 0, err
	}

	readBuffer, err := datareader.ReadBytes(bufferlen)
	if err != nil {
		return 0, err
	}

	copy(data, readBuffer)
	return len(readBuffer), nil
}

// EnableNotifications enables notifications or indicate in the Client Characteristic
// Configuration Descriptor (CCCD). And it favors Notify over Indicate.
func (c DeviceCharacteristic) EnableNotifications(callback func(buf []byte)) error {
	return c.EnableNotificationsWithContext(context.Background(), callback)
}

// EnableNotificationsWithContext enables notifications or indicate in the Client Characteristic
// Configuration Descriptor (CCCD). And it favors Notify over Indicate.
func (c DeviceCharacteristic) EnableNotificationsWithContext(ctx context.Context, callback func(buf []byte)) error {
	var err error
	if c.properties&genericattributeprofile.GattCharacteristicPropertiesNotify != 0 {
		err = c.EnableNotificationsWithMode(ctx, NotificationModeNotify, callback)
	} else if c.properties&genericattributeprofile.GattCharacteristicPropertiesIndicate != 0 {
		err = c.EnableNotificationsWithMode(ctx, NotificationModeIndicate, callback)
	} else {
		return errNoNotifyOrIndicate
	}

	if err != nil {
		return err
	}
	return nil
}

// EnableNotificationsWithMode enables notifications in the Client Characteristic
// Configuration Descriptor (CCCD). This means that most peripherals will send a
// notification with a new value every time the value of the characteristic
// changes. And you can select the notify/indicate mode as you need.
func (c DeviceCharacteristic) EnableNotificationsWithMode(ctx context.Context, mode NotificationMode, callback func(buf []byte)) error {
	configValue := genericattributeprofile.GattClientCharacteristicConfigurationDescriptorValueNone
	if mode == NotificationModeIndicate {
		if c.properties&genericattributeprofile.GattCharacteristicPropertiesIndicate == 0 {
			return errNoIndicate
		}
		// set to indicate mode
		configValue = genericattributeprofile.GattClientCharacteristicConfigurationDescriptorValueIndicate
	} else if mode == NotificationModeNotify {
		if c.properties&genericattributeprofile.GattCharacteristicPropertiesNotify == 0 {
			return errNoNotify
		}
		// set to notify mode
		configValue = genericattributeprofile.GattClientCharacteristicConfigurationDescriptorValueNotify
	} else {
		return errInvalidNotificationMode
	}

	// listen value changed event
	// TypedEventHandler<GattCharacteristic,GattValueChangedEventArgs>
	guid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler, genericattributeprofile.SignatureGattCharacteristic, genericattributeprofile.SignatureGattValueChangedEventArgs)
	valueChangedEventHandler := foundation.NewTypedEventHandler(ole.NewGUID(guid), func(instance *foundation.TypedEventHandler, sender, args unsafe.Pointer) {
		valueChangedEvent := (*genericattributeprofile.GattValueChangedEventArgs)(args)

		buf, err := valueChangedEvent.GetCharacteristicValue()
		if err != nil {
			return
		}

		reader, err := streams.DataReaderFromBuffer(buf)
		if err != nil {
			return
		}
		defer reader.Release()

		buflen, err := buf.GetLength()
		if err != nil {
			return
		}

		data, err := reader.ReadBytes(buflen)
		if err != nil {
			return
		}

		callback(data)
	})
	_, err := c.characteristic.AddValueChanged(valueChangedEventHandler)
	if err != nil {
		return err
	}

	writeOp, err := c.characteristic.WriteClientCharacteristicConfigurationDescriptorAsync(configValue)
	if err != nil {
		return err
	}

	// IAsyncOperation<GattCommunicationStatus>
	if err := awaitAsyncOperation(ctx, writeOp, genericattributeprofile.SignatureGattCommunicationStatus); err != nil {
		return err
	}

	res, err := writeOp.GetResults()
	if err != nil {
		return err
	}

	result := genericattributeprofile.GattCommunicationStatus(uintptr(res))

	if result != genericattributeprofile.GattCommunicationStatusSuccess {
		return errEnableNotificationsFailed
	}

	return nil
}

// DisableNotifications disables notifications from this characteristic.
func (c DeviceCharacteristic) DisableNotifications() error {
	// Set CCCD to None to disable notifications/indications
	writeOp, err := c.characteristic.WriteClientCharacteristicConfigurationDescriptorAsync(
		genericattributeprofile.GattClientCharacteristicConfigurationDescriptorValueNone)
	if err != nil {
		return err
	}

	// IAsyncOperation<GattCommunicationStatus>
	if err := awaitAsyncOperation(context.TODO(), writeOp, genericattributeprofile.SignatureGattCommunicationStatus); err != nil {
		return err
	}

	res, err := writeOp.GetResults()
	if err != nil {
		return err
	}

	result := genericattributeprofile.GattCommunicationStatus(uintptr(res))

	if result != genericattributeprofile.GattCommunicationStatusSuccess {
		return fmt.Errorf("bluetooth: disable notifications failed with status %d", result)
	}

	return nil
}
