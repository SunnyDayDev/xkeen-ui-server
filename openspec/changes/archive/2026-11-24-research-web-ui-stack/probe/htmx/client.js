const rows = document.querySelector('#rows')
document.querySelector('#swap').addEventListener('click', () => rows.append(rows.firstElementChild))
