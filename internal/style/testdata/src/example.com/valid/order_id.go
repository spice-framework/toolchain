package valid

type OrderID string

func ParseOrderID(value string) OrderID {
	return OrderID(value)
}
