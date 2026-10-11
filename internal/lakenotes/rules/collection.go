package rules

// CollectionProgress uses caught records rather than the current fish basket.
func CollectionProgress(p Profile, location string) (caught, total int, valid bool) {
	if _, valid = catalog.Locations[location]; !valid {
		return
	}
	for _, fish := range catalog.Fish {
		if fish.Location == location {
			total++
			if record, ok := p.Records[fish.Kind]; ok && record.Caught != "0" {
				caught++
			}
		}
	}
	return
}
