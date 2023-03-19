# Layerd - L3VPN Solution

- Automatic route distribution
- Up to 255 separate layer 3 networks

## Running
1) Ensure each node can reach each other. Nodes the program is running on must be connected in a chain, circle or mesh form
2) Edit config.ini and optionally edit the 'announcements' file to add a list of prefixes for each node
3) Run via ./layerd. The program requires root or the CAP_NET_ADMIN capability 
4) Access the cli via ./layerd cli (View/Announce/Withdraw routes)

The program will create a separate interface for each layer 3 network ID specified in the 'config.ini' file.
